package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
	"github.com/Spudifull/kubernetes-application-lab/internal/worker"
	"github.com/twmb/franz-go/pkg/kgo"
)

// crashConsumer intercepts the commit boundary, after Processor has persisted
// or recognized the SQL effect. Exit deliberately skips graceful group leave.
// The target also crashes on redelivery until the operator disarms it, keeping
// the uncommitted offset available for inspection across container restarts.
type crashConsumer struct {
	worker.Consumer
	jobID  string
	logger *slog.Logger
	exit   func(int)
}

func (c crashConsumer) CommitRecords(ctx context.Context, records ...*kgo.Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, r := range records {
		e, err := jobs.DecodeEvent(r.Key, r.Value)
		if err != nil {
			return err
		}
		if c.jobID != "" && e.JobID == c.jobID {
			c.logger.Error("worker_crash_before_offset_commit", "job_id", e.JobID, "event_id", e.EventID,
				"topic", r.Topic, "partition", r.Partition, "offset", r.Offset, "group", worker.Group, "exit_code", 86)
			c.exit(86)
			return errors.New("injected worker crash returned unexpectedly")
		}
	}
	return c.Consumer.CommitRecords(ctx, records...)
}
