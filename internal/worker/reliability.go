package worker

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
	"github.com/Spudifull/kubernetes-application-lab/internal/telemetry"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var ErrRetryPending = errors.New("worker retry pending")

type ReliabilityStore interface {
	RetryState(context.Context, jobs.Event) (jobs.RetryState, error)
	RecordFailure(context.Context, jobs.Event, int) error
	PrepareDeadLetter(context.Context, jobs.DeadLetter) (bool, error)
	DeadLetterPublished(context.Context, jobs.DeadLetter, int32, int64) error
}

type DLQPublisher interface {
	Publish(context.Context, jobs.DeadLetter) (int32, int64, error)
}

type Reliability struct {
	Store     ReliabilityStore
	Publisher DLQPublisher
	Metrics   *telemetry.Metrics
}

func deadLetter(r *kgo.Record, e *jobs.Event, reason string) jobs.DeadLetter {
	d := jobs.DeadLetter{Version: 1, SourceTopic: r.Topic, SourcePartition: r.Partition, SourceOffset: r.Offset,
		Reason: reason, ValueBytes: len(r.Value), ValueSHA256: fmt.Sprintf("%x", sha256.Sum256(r.Value)), KeySHA256: fmt.Sprintf("%x", sha256.Sum256(r.Key))}
	if e != nil {
		d.JobID, d.EventID, d.Failures = e.JobID, e.EventID, jobs.MaxWorkerFailures
	}
	return d
}

func (p Processor) reject(ctx context.Context, r *kgo.Record, e *jobs.Event, reason string) error {
	if p.Reliability == nil {
		return jobs.ErrEvent
	}
	trace.SpanFromContext(ctx).AddEvent("dead_letter", trace.WithAttributes(attribute.String("lab.reason", reason)))
	d := deadLetter(r, e, reason)
	published, err := p.Reliability.Store.PrepareDeadLetter(ctx, d)
	if err != nil {
		return err
	}
	if !published {
		partition, offset, err := p.Reliability.Publisher.Publish(ctx, d)
		if err != nil {
			p.Logger.Warn("worker_dlq_publish_failed", "source", d.Key(), "reason", reason)
			return err // Never advance the source offset on DLQ failure.
		}
		p.Logger.Info("worker_dlq_broker_ack", "source", d.Key(), "dlq_partition", partition, "dlq_offset", offset)
		if err := p.Reliability.Store.DeadLetterPublished(ctx, d, partition, offset); err != nil {
			return err // Ack-before-SQL crash may redeliver the same DLQ identity.
		}
		p.Metrics.Add("jobs_dlq")
	}
	p.Logger.Info("worker_dlq_recorded", "source", d.Key(), "reason", reason, "receipt_already_present", published)
	return p.commit(ctx, r, []any{"topic", r.Topic, "partition", r.Partition, "offset", r.Offset, "group", Group})
}
