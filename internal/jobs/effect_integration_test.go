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

func TestPostgresWorker(t *testing.T) {
	if os.Getenv("LAB_JOBS_INTEGRATION") != "1" {
		t.Skip("explicit isolated PostgreSQL opt-in required")
	}
	config, err := database.ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(config.Name, "lab_jobs_test_") {
		t.Fatal("main database refused; use lab_jobs_test_*")
	}
	db, err := database.Open(config)
	if err != nil {
		t.Fatal("open isolated DB")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := database.Migrate(ctx, db, false); err != nil {
		t.Fatal(err)
	}
	unique, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	store := Store{DB: db}
	job, _, err := store.Enqueue(ctx, Request{IdempotencyKey: "effect-" + unique, JobType: "lab_effect", Payload: Payload{1000}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		for _, query := range []string{`DELETE FROM public.lab_job_effects WHERE job_id=$1`, `DELETE FROM public.lab_outbox WHERE job_id=$1`, `DELETE FROM public.lab_jobs WHERE id=$1`} {
			if _, err := db.ExecContext(cleanup, query, job.ID); err != nil {
				t.Errorf("test-row cleanup: %v", err)
			}
		}
	}()
	var body []byte
	if err := db.QueryRowContext(ctx, `SELECT payload FROM public.lab_outbox WHERE job_id=$1`, job.ID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	event, err := DecodeEvent([]byte(job.ID), body)
	if err != nil {
		t.Fatal(err)
	}
	mismatch := event
	mismatch.Payload.DurationMS++
	if _, err := store.Start(ctx, mismatch); !errors.Is(err, ErrEvent) {
		t.Fatal("mismatched event accepted", err)
	}
	if duplicate, err := store.Start(ctx, event); err != nil || duplicate {
		t.Fatal("first start", err)
	}
	const count = 8
	var wg sync.WaitGroup
	gate := make(chan struct{})
	duplicates := make([]bool, count)
	errs := make([]error, count)
	for i := range count {
		wg.Add(1)
		go func() { defer wg.Done(); <-gate; duplicates[i], errs[i] = store.Complete(ctx, event) }()
	}
	close(gate)
	wg.Wait()
	created := 0
	for i := range count {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if !duplicates[i] {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("effects applied=%d", created)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM public.lab_job_effects WHERE job_id=$1`, job.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("effects=%d err=%v", n, err)
	}
	completed, err := store.Get(ctx, job.ID)
	if err != nil || completed.Status != "succeeded" || completed.CompletedAt == nil {
		t.Fatal("completion not persisted", err)
	}
	var result Effect
	if json.Unmarshal(completed.Result, &result) != nil || result.Outcome != "completed" || result.DurationMS != 1000 {
		t.Fatal("unexpected result")
	}
	if duplicate, err := store.Start(ctx, event); err != nil || !duplicate {
		t.Fatal("completed job not deduplicated", err)
	}
	before := *completed.CompletedAt
	if duplicate, err := store.Complete(ctx, event); err != nil || !duplicate {
		t.Fatal("duplicate completion", err)
	}
	completed, err = store.Get(ctx, job.ID)
	if err != nil || !completed.CompletedAt.Equal(before) {
		t.Fatal("duplicate mutated completion time", err)
	}
}
