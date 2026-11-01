package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
	"github.com/Spudifull/kubernetes-application-lab/internal/worker"
	"github.com/twmb/franz-go/pkg/kgo"
)

type crashTestConsumer struct{ commits int }

func (*crashTestConsumer) PollRecords(context.Context, int) kgo.Fetches { return nil }
func (*crashTestConsumer) AllowRebalance()                              {}
func (c *crashTestConsumer) CommitRecords(context.Context, ...*kgo.Record) error {
	c.commits++
	return nil
}

type crashTestStore struct{ effects int }

func (s *crashTestStore) Start(context.Context, jobs.Event) (bool, error) {
	return s.effects > 0, nil
}
func (s *crashTestStore) Complete(context.Context, jobs.Event) (bool, error) {
	s.effects++
	return false, nil
}

func TestCrashBoundaryAndDisarmedRedelivery(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000002"
	r := &kgo.Record{Topic: jobs.Topic, Key: []byte(id), Partition: 1, Offset: 7,
		Value: []byte(`{"schema_version":1,"event_id":"00000000-0000-4000-8000-000000000001","job_id":"00000000-0000-4000-8000-000000000002","job_type":"lab_effect","created_at":"2026-10-25T00:00:00Z","payload":{"duration_ms":1}}`)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := &crashTestStore{}
	consumer := &crashTestConsumer{}
	crashes := 0
	fault := crashConsumer{Consumer: consumer, jobID: id, logger: logger, exit: func(code int) {
		if code != 86 || store.effects != 1 || consumer.commits != 0 {
			t.Fatalf("exit=%d effects=%d commits=%d", code, store.effects, consumer.commits)
		}
		crashes++
	}}
	p := worker.Processor{Store: store, Committer: fault, Logger: logger, Work: func(context.Context, time.Duration) error { return nil }}
	// Model durable SQL across process restarts. Both the first effect and an
	// armed duplicate must stop before commit; fake exit returning must not leak it.
	for range 2 {
		if err := p.Handle(context.Background(), r); err == nil {
			t.Fatal("injected exit returned success")
		}
	}
	if crashes != 2 || consumer.commits != 0 || store.effects != 1 {
		t.Fatalf("crashes=%d commits=%d effects=%d", crashes, consumer.commits, store.effects)
	}
	p.Committer = consumer // Recovery disables the fault for the same record.
	if err := p.Handle(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if consumer.commits != 1 || store.effects != 1 {
		t.Fatalf("commits=%d effects=%d", consumer.commits, store.effects)
	}
	// An unrelated job must remain committable while the target is armed.
	fault.jobID = "00000000-0000-4000-8000-000000000003"
	if err := fault.CommitRecords(context.Background(), r); err != nil || consumer.commits != 2 || crashes != 2 {
		t.Fatalf("unrelated record intercepted: err=%v commits=%d crashes=%d", err, consumer.commits, crashes)
	}
}
