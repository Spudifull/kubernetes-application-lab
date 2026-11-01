// Package worker owns the ordering of PostgreSQL effects and Kafka commits.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
	"github.com/Spudifull/kubernetes-application-lab/internal/telemetry"
	"github.com/Spudifull/kubernetes-application-lab/internal/tracing"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const Group = "lab-workers"

type Store interface {
	Start(context.Context, jobs.Event) (bool, error)
	Complete(context.Context, jobs.Event) (bool, error)
}
type Committer interface {
	CommitRecords(context.Context, ...*kgo.Record) error
}

type Processor struct {
	Store       Store
	Committer   Committer
	Logger      *slog.Logger
	Work        func(context.Context, time.Duration) error
	Reliability *Reliability
	Metrics     *telemetry.Metrics
}

func Wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

// Handle never commits an offset on validation, work or persistence failure.
// Its caller stops the current consumer session on any error, so a later poll
// cannot skip past this record even though PollRecords advances the local position.
func (p Processor) Handle(ctx context.Context, r *kgo.Record) (handleErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx = tracing.KafkaContext(ctx, r.Headers)
	ctx, processSpan := tracing.Start(ctx, "lab.jobs.v1 process", trace.SpanKindConsumer,
		attribute.String("messaging.system", "kafka"), attribute.String("messaging.destination.name", jobs.Topic),
		attribute.String("messaging.consumer.group.name", Group), attribute.Int64("messaging.kafka.offset", r.Offset), attribute.Int("messaging.destination.partition.id", int(r.Partition)))
	defer func() {
		if errors.Is(handleErr, context.Canceled) || errors.Is(handleErr, context.DeadlineExceeded) {
			processSpan.AddEvent("attempt_canceled")
		}
		if errors.Is(handleErr, ErrRetryPending) {
			processSpan.SetAttributes(attribute.String("lab.outcome", "retry_pending"))
			processSpan.End()
			return
		}
		tracing.End(processSpan, handleErr, "worker_attempt_incomplete")
	}()
	p.Logger = p.Logger.With(tracing.Fields(ctx)...)
	finish := p.Metrics.Begin()
	defer finish()
	defer func() {
		if handleErr != nil && !errors.Is(handleErr, ErrRetryPending) && !errors.Is(handleErr, context.Canceled) {
			p.Metrics.Add("jobs_failed")
		}
	}()
	e, err := jobs.DecodeEvent(r.Key, r.Value)
	if r.Topic != jobs.Topic {
		return jobs.ErrEvent
	}
	if err != nil {
		p.Logger.Error("worker_event_rejected", "topic", r.Topic, "partition", r.Partition, "offset", r.Offset, "bytes", len(r.Value))
		return p.reject(ctx, r, nil, "invalid_event")
	}
	fields := []any{"job_id", e.JobID, "event_id", e.EventID, "topic", r.Topic, "partition", r.Partition, "offset", r.Offset, "group", Group}
	processSpan.SetAttributes(attribute.String("job.id", e.JobID), attribute.String("messaging.message.id", e.EventID))
	p.Logger.Info("worker_received", fields...)
	duplicate, err := p.Store.Start(ctx, e)
	if err != nil {
		if errors.Is(err, jobs.ErrJobFailed) {
			return p.reject(ctx, r, &e, "retry_exhausted")
		}
		if errors.Is(err, jobs.ErrEvent) {
			return p.reject(ctx, r, nil, "inconsistent_event")
		}
		p.Logger.Warn("worker_start_failed", fields...)
		return err
	}
	if !duplicate {
		var retry jobs.RetryState
		if p.Reliability != nil {
			retry, err = p.Reliability.Store.RetryState(ctx, e)
			if err != nil {
				return err
			}
			if retry.Failures >= jobs.MaxWorkerFailures {
				return p.reject(ctx, r, &e, "retry_exhausted")
			}
			if retry.Wait > 0 {
				p.Logger.Info("worker_retry_wait", append(fields, "remaining", retry.Wait)...)
				return ErrRetryPending
			}
		}
		p.Logger.Info("worker_work_started", fields...)
		work := p.Work
		if work == nil {
			work = Wait
		}
		workCtx, workSpan := tracing.Start(ctx, "worker.work", trace.SpanKindInternal)
		err = work(workCtx, time.Duration(e.Payload.DurationMS)*time.Millisecond)
		tracing.End(workSpan, err, "work_canceled")
		if err != nil {
			p.Logger.Info("worker_work_canceled", fields...)
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if p.Reliability != nil && retry.Failures < retry.FailuresBeforeSuccess {
			if err := p.Reliability.Store.RecordFailure(ctx, e, retry.Failures); err != nil {
				return err
			}
			p.Metrics.Add("jobs_failed")
			p.Metrics.Add("jobs_retried")
			p.Logger.Info("worker_retry_scheduled", append(fields, "failure_number", retry.Failures+1, "max_failures", jobs.MaxWorkerFailures)...)
			processSpan.AddEvent("retry_scheduled", trace.WithAttributes(attribute.Int("lab.failure_number", retry.Failures+1)))
			return ErrRetryPending
		}
		duplicate, err = p.Store.Complete(ctx, e)
		if err != nil {
			p.Logger.Warn("worker_effect_incomplete", fields...)
			return err
		}
		p.Logger.Info("worker_effect_committed", append(fields, "duplicate", duplicate)...)
		if duplicate {
			processSpan.AddEvent("duplicate_observed")
			p.Metrics.Add("jobs_duplicates")
		} else {
			p.Metrics.Add("jobs_processed")
		}
	} else {
		processSpan.AddEvent("duplicate_observed")
		p.Metrics.Add("jobs_duplicates")
		p.Logger.Info("worker_duplicate_ignored", fields...)
	}
	return p.commit(ctx, r, fields)
}

func (p Processor) commit(ctx context.Context, r *kgo.Record, fields []any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	commitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	commitCtx, span := tracing.Start(commitCtx, "lab-workers commit", trace.SpanKindClient, attribute.String("messaging.system", "kafka"))
	err := p.Committer.CommitRecords(commitCtx, r)
	tracing.End(span, err, "offset_commit_failed")
	if err != nil {
		p.Metrics.Add("kafka_offset_commit_failures")
		p.Logger.Warn("worker_offset_commit_failed", fields...)
		return err
	}
	p.Metrics.Add("kafka_offset_commits")
	p.Logger.Info("worker_offset_committed", append(fields, "committed_offset", r.Offset+1)...)
	return nil
}

type Consumer interface {
	PollRecords(context.Context, int) kgo.Fetches
	AllowRebalance()
	Committer
}

// Consume processes one record globally at a time. Ordinary rebalances stay
// blocked only during one bounded attempt (18s incl. DB/work/offset commit).
// SIGTERM cancels polling but not an accepted attempt; rebalance signals cancel
// that attempt via Gate so revoke/lost can finish without post-revoke commits.
func Consume(ctx context.Context, c Consumer, store Store, gate *Gate, logger *slog.Logger, reliability ...*Reliability) error {
	var policy *Reliability
	if len(reliability) > 0 {
		policy = reliability[0]
	}
	for ctx.Err() == nil {
		fetches := c.PollRecords(ctx, 1)
		if ctx.Err() != nil {
			c.AllowRebalance()
			return nil
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			c.AllowRebalance()
			return errors.New("Kafka fetch failed")
		}
		records := fetches.Records()
		if len(records) == 0 {
			c.AllowRebalance()
			continue
		}
		attempt, cancel := context.WithTimeout(context.WithoutCancel(ctx), 18*time.Second)
		gate.bind(cancel)
		var metrics *telemetry.Metrics
		if policy != nil {
			metrics = policy.Metrics
		}
		err := (Processor{Store: store, Committer: c, Logger: logger, Reliability: policy, Metrics: metrics}).Handle(attempt, records[0])
		gate.unbind()
		cancel()
		c.AllowRebalance()
		if err != nil {
			return err
		} // Caller recreates the client at committed offsets.
	}
	return nil
}
