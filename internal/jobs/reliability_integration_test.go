//go:build integration

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/database"
)

func TestPostgresWorkerReliability(t *testing.T) {
	if os.Getenv("LAB_JOBS_INTEGRATION") != "1" {
		t.Skip("explicit isolated PostgreSQL opt-in required")
	}
	config, err := database.ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(config.Name, "lab_jobs_test_") {
		t.Fatal("main database refused")
	}
	db, err := database.Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for range 2 {
		if err := database.Migrate(ctx, db, false); err != nil {
			t.Fatal(err)
		}
	}
	s := Store{DB: db}
	if err := s.WorkerReady(ctx); err != nil {
		t.Fatal(err)
	}
	unique, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	j, _, err := s.Enqueue(ctx, Request{IdempotencyKey: "reliable-" + unique, JobType: "lab_effect", Payload: Payload{DurationMS: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		for _, q := range []string{`DELETE FROM public.lab_job_effects WHERE job_id=$1`, `DELETE FROM public.lab_outbox WHERE job_id=$1`, `DELETE FROM public.lab_jobs WHERE id=$1`} {
			if _, err := db.ExecContext(cleanup, q, j.ID); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
	}()
	var body []byte
	if err := db.QueryRowContext(ctx, `SELECT payload FROM public.lab_outbox WHERE job_id=$1`, j.ID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	e, err := DecodeEvent([]byte(j.ID), body)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureWorkerFault(ctx, j.ID, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, e); err != nil {
		t.Fatal(err)
	}
	// Overlapping consumers observed the same failure count. Only one increment
	// may persist, even though all calls may finish successfully as stale retries.
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = s.RecordFailure(ctx, e, 0) }()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	state, err := (Store{DB: db}).RetryState(ctx, e)
	if err != nil || state.Failures != 1 || state.Wait <= 0 || state.FailuresBeforeSuccess != 3 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if err := s.ConfigureWorkerFault(ctx, j.ID, 1); err == nil {
		t.Fatal("fault reconfigured after work started")
	}
	for previous := 1; previous < 3; previous++ {
		if err := s.RecordFailure(ctx, e, previous); err != nil {
			t.Fatal(err)
		}
	}
	state, err = s.RetryState(ctx, e)
	if err != nil || state.Failures != 3 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	// Concurrent source positions must not collide with existing fixtures.
	var source int64
	if err := db.QueryRowContext(ctx, `SELECT coalesce(max(source_offset),0)+1 FROM public.lab_dead_letters WHERE source_topic=$1 AND source_partition=0`, Topic).Scan(&source); err != nil {
		t.Fatal(err)
	}
	d := DeadLetter{Version: 1, SourceTopic: Topic, SourcePartition: 0, SourceOffset: source, JobID: e.JobID, EventID: e.EventID, Reason: "retry_exhausted", Failures: 3}
	if done, err := s.PrepareDeadLetter(ctx, d); err != nil || done {
		t.Fatalf("prepare done=%v err=%v", done, err)
	}
	before, err := s.Get(ctx, j.ID)
	if err != nil || before.Status != "processing" || before.CompletedAt != nil {
		t.Fatal("pending DLQ changed terminal state", err)
	}
	if err := s.DeadLetterPublished(ctx, d, 1, 4); err != nil {
		t.Fatal(err)
	}
	after, err := s.Get(ctx, j.ID)
	if err != nil || after.Status != "failed" || after.CompletedAt == nil || after.ErrorCode == nil || *after.ErrorCode != "retry_exhausted" {
		t.Fatalf("job=%+v err=%v", after, err)
	}
	if _, err := s.Start(ctx, e); !errors.Is(err, ErrJobFailed) {
		t.Fatal("failed job can execute again", err)
	}
	if _, err := s.Complete(ctx, e); !errors.Is(err, ErrJobFailed) {
		t.Fatal("failed job can create effect", err)
	}
	if done, err := s.PrepareDeadLetter(ctx, d); err != nil || !done {
		t.Fatal("receipt not durable", err)
	}
	if err := s.DeadLetterPublished(ctx, d, 2, 8); err != nil {
		t.Fatal(err)
	}
	again, err := s.Get(ctx, j.ID)
	if err != nil || !again.CompletedAt.Equal(*after.CompletedAt) {
		t.Fatal("duplicate receipt mutated completion", err)
	}
	var partition int32
	var offset int64
	if err := db.QueryRowContext(ctx, `SELECT dlq_partition,dlq_offset FROM public.lab_dead_letters WHERE job_id=$1`, j.ID).Scan(&partition, &offset); err != nil || partition != 1 || offset != 4 {
		t.Fatal("first receipt overwritten", err)
	}
	conflicting := d
	conflicting.Reason = "different"
	if _, err := s.PrepareDeadLetter(ctx, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatal("source identity conflict hidden", err)
	}
	// Invalid source can be recorded without any job FK or mutation of jobs.
	poison := DeadLetter{Version: 1, SourceTopic: Topic, SourcePartition: 0, SourceOffset: source + 1, Reason: "invalid_event"}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		if _, err := db.ExecContext(cleanup, `DELETE FROM public.lab_dead_letters WHERE source_topic=$1 AND source_partition=$2 AND source_offset=$3`, poison.SourceTopic, poison.SourcePartition, poison.SourceOffset); err != nil {
			t.Errorf("poison cleanup: %v", err)
		}
	}()
	if done, err := s.PrepareDeadLetter(ctx, poison); err != nil || done {
		t.Fatal("poison without job rejected", err)
	}
	if err := s.DeadLetterPublished(ctx, poison, 0, 0); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(poison)
	if err != nil || strings.Contains(string(encoded), "job_id") {
		t.Fatal("poison invented a job")
	}
}
