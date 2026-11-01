// Package publisher owns the ack-before-mark ordering, not job processing.
package publisher

import (
	"context"
	"database/sql"
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

type Store interface {
	Claim(context.Context) (jobs.Claim, error)
	Published(context.Context, jobs.Claim, int32, int64) error
	Retry(context.Context, jobs.Claim) error
}

type Producer interface {
	ProduceSync(context.Context, ...*kgo.Record) kgo.ProduceResults
}

type Publisher struct {
	Store    Store
	Producer Producer
	Logger   *slog.Logger
	Metrics  *telemetry.Metrics
}

// Step has no hidden retries; a persisted claim attempt is bounded by its caller.
// A failed DB mark after Kafka ack intentionally leaves a recoverable lease.
func (p Publisher) Step(ctx context.Context) (stepErr error) {
	claimStart := time.Now()
	claim, err := p.Store.Claim(ctx)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			p.Metrics.Add("claim_errors")
		}
		return err
	}
	claimEnd := time.Now()
	ctx = tracing.Extract(ctx, claim.TraceParent)
	ctx, attemptSpan := tracing.StartAt(ctx, "outbox.publish_attempt", claimStart, trace.SpanKindInternal,
		attribute.String("job.id", claim.JobID), attribute.String("messaging.message.id", claim.EventID), attribute.Int64("lab.attempt", claim.Attempt))
	defer func() { tracing.End(attemptSpan, stepErr, "publisher_attempt_failed") }()
	// Claim must precede extraction. Reconstruct only this completed SQL span,
	// never an idle polling trace; start/end retain the actual measured interval.
	_, claimSpan := attemptSpan.TracerProvider().Tracer("lab").Start(ctx, "outbox.claim", trace.WithSpanKind(trace.SpanKindClient), trace.WithTimestamp(claimStart), trace.WithAttributes(attribute.String("db.system.name", "postgresql")))
	claimSpan.End(trace.WithTimestamp(claimEnd))
	p.Logger = p.Logger.With(tracing.Fields(ctx)...)
	p.Metrics.Add("attempts")
	if claim.Attempt > 1 {
		p.Metrics.Add("retries")
	}
	finish := p.Metrics.Begin()
	defer finish()
	fields := []any{"event_id", claim.EventID, "job_id", claim.JobID, "attempt", claim.Attempt, "topic", claim.Topic}
	p.Logger.Info("outbox_claimed", fields...)
	publishCtx, publishSpan := tracing.Start(ctx, "lab.jobs.v1 publish", trace.SpanKindProducer, attribute.String("messaging.system", "kafka"), attribute.String("messaging.destination.name", jobs.Topic))
	record := &kgo.Record{Topic: claim.Topic, Key: []byte(claim.JobID), Value: claim.Payload, Headers: tracing.KafkaHeaders(publishCtx)}
	result := p.Producer.ProduceSync(publishCtx, record)
	tracing.End(publishSpan, result.FirstErr(), "kafka_publish_failed")
	if err := result.FirstErr(); err != nil {
		attemptSpan.AddEvent("retry_scheduled", trace.WithAttributes(attribute.Int64("lab.retry_delay_ms", jobs.RetryDelay(claim.Attempt).Milliseconds())))
		attemptSpan.SetAttributes(attribute.String("lab.outcome", "retry_scheduled"))
		p.Metrics.Add("publish_errors")
		// Do not log library errors: broker responses may contain payload details.
		p.Logger.Warn("outbox_publish_failed", append(fields, "reason", "kafka_publish_failed", "retry_delay", jobs.RetryDelay(claim.Attempt))...)
		// Allow a bounded DB release even if delivery exhausted the attempt context.
		retryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if retryErr := p.Store.Retry(retryCtx, claim); retryErr != nil {
			return retryErr
		}
		return nil
	}
	p.Metrics.Add("publish_success")
	p.Metrics.Ack()
	attemptSpan.AddEvent("broker_ack", trace.WithAttributes(attribute.Int64("messaging.kafka.offset", record.Offset), attribute.Int("messaging.destination.partition.id", int(record.Partition))))
	p.Logger.Info("outbox_broker_ack", append(fields, "partition", record.Partition, "offset", record.Offset)...)
	if err := p.Store.Published(ctx, claim, record.Partition, record.Offset); err != nil {
		p.Metrics.Add("outbox_mark_errors")
		return err
	}
	p.Metrics.Add("outbox_published")
	p.Logger.Info("outbox_published", append(fields, "partition", record.Partition, "offset", record.Offset)...)
	return nil
}

// Run stops claiming on SIGTERM, but lets the single accepted attempt finish.
// 25s operation + at most 2s cleanup is below the 35s Pod termination budget.
func (p Publisher) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if ctx.Err() != nil {
			return
		}
		attemptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 25*time.Second)
		err := p.Step(attemptCtx)
		cancel()
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			reason := "outbox_database_failed"
			if errors.Is(err, jobs.ErrLeaseLost) {
				reason = "claim_lost"
			}
			p.Logger.Warn("outbox_attempt_incomplete", "reason", reason)
		}
		timer.Reset(time.Second)
	}
}
