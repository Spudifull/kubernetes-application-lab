//go:build integration

package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/database"
	"github.com/Spudifull/kubernetes-application-lab/internal/tracing"
)

// This test never creates/drops a database and refuses the laboratory's main DB.
// The owner must provide an isolated local DB, not run this against production.
func TestPostgresOutbox(t *testing.T) {
	if os.Getenv("LAB_JOBS_INTEGRATION") != "1" {
		t.Skip("explicit isolated PostgreSQL opt-in required")
	}
	config, err := database.ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(config.Name, "lab_jobs_test_") {
		t.Fatal("LAB_DB_NAME must start with lab_jobs_test_; main database refused")
	}
	db, err := database.Open(config)
	if err != nil {
		t.Fatal("open isolated database")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := database.Migrate(ctx, db, false); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, db, false); err != nil {
		t.Fatal("migration not repeatable", err)
	}
	store := Store{db}
	if err := store.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if pending, age, err := store.OutboxStats(ctx); err != nil || pending != 0 || age != 0 {
		t.Fatalf("initial outbox snapshot: pending=%d age=%g err=%v (use an empty isolated DB)", pending, age, err)
	}
	unique, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	const parent = "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"
	ctx = tracing.Extract(ctx, parent)
	key := "test-" + unique
	request := Request{key, "lab_effect", Payload{1000}}
	const count = 8
	results := make([]Job, count)
	errs := make([]error, count)
	var wg sync.WaitGroup
	gate := make(chan struct{})
	for i := range count {
		wg.Add(1)
		go func() { defer wg.Done(); <-gate; results[i], _, errs[i] = store.Enqueue(ctx, request) }()
	}
	close(gate)
	wg.Wait()
	for i := range count {
		if errs[i] != nil || results[i].ID != results[0].ID {
			t.Fatalf("concurrent enqueue %d: %v", i, errs[i])
		}
	}
	id := results[0].ID
	// Cleanup is limited to rows created by this test; never clear shared tables.
	defer func() {
		for _, query := range []string{`DELETE FROM public.lab_job_effects WHERE job_id=$1`, `DELETE FROM public.lab_outbox WHERE job_id=$1`, `DELETE FROM public.lab_jobs WHERE id=$1`} {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
			_, err := db.ExecContext(cleanupCtx, query, id)
			stop()
			if err != nil {
				t.Errorf("test-row cleanup: %v", err)
			}
		}
	}()
	if pending, age, err := store.OutboxStats(ctx); err != nil || pending != 1 || age < 0 {
		t.Fatalf("pending snapshot: pending=%d age=%g err=%v", pending, age, err)
	}
	request.Payload.DurationMS++
	if _, _, err := store.Enqueue(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatal("different content must conflict", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM public.lab_outbox WHERE job_id=$1`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("outbox rows=%d err=%v", n, err)
	}
	var savedParent string
	if err := db.QueryRowContext(ctx, `SELECT trace_parent FROM public.lab_outbox WHERE job_id=$1`, id).Scan(&savedParent); err != nil || savedParent != parent {
		t.Fatalf("durable trace parent=%q err=%v", savedParent, err)
	}
	// A retry from a different HTTP trace must preserve the first durable context.
	other := tracing.Extract(ctx, "00-fedcba9876543210fedcba9876543210-fedcba9876543210-01")
	request.Payload.DurationMS--
	if _, created, err := store.Enqueue(other, request); err != nil || created {
		t.Fatalf("idempotent trace replay: created=%v err=%v", created, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT trace_parent FROM public.lab_outbox WHERE job_id=$1`, id).Scan(&savedParent); err != nil || savedParent != parent {
		t.Fatal("idempotent retry replaced original trace", err)
	}
	var eventJSON []byte
	if err := db.QueryRowContext(ctx, `SELECT payload FROM public.lab_outbox WHERE job_id=$1`, id).Scan(&eventJSON); err != nil {
		t.Fatal(err)
	}
	var event Event
	if err := json.Unmarshal(eventJSON, &event); err != nil {
		t.Fatal(err)
	}
	_, zoneOffset := event.CreatedAt.Zone()
	if event.SchemaVersion != 1 || event.JobID != id || !ValidID(event.EventID) || event.JobType != "lab_effect" || event.Payload.DurationMS != 1000 || event.CreatedAt.IsZero() || zoneOffset != 0 {
		t.Fatal("persisted event does not match v1 contract")
	}
	claims := make([]Claim, count)
	gate = make(chan struct{})
	for i := range count {
		wg.Add(1)
		go func() { defer wg.Done(); <-gate; claims[i], errs[i] = store.Claim(ctx) }()
	}
	close(gate)
	wg.Wait()
	var first Claim
	winners := 0
	for i := range count {
		if errs[i] == nil {
			winners++
			first = claims[i]
		} else if !errors.Is(errs[i], sql.ErrNoRows) {
			t.Fatal(errs[i])
		}
	}
	if winners != 1 || first.JobID != id || first.EventID != event.EventID {
		t.Fatalf("claim winners=%d (use a dedicated empty test DB)", winners)
	}
	if pending, _, err := store.OutboxStats(ctx); err != nil || pending != 1 {
		t.Fatalf("leased event disappeared from pending: %d %v", pending, err)
	}
	if _, err := store.Claim(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("live lease was claimed twice", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE public.lab_outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE event_id=$1`, first.EventID); err != nil {
		t.Fatal(err)
	}
	second, err := store.Claim(ctx)
	if err != nil || second.Token == first.Token || second.EventID != first.EventID {
		t.Fatal("expired claim not recovered", err)
	}
	if err := store.Published(ctx, first, 0, 10); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("old token marked publication", err)
	}
	if err := store.Retry(ctx, first); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("old token released new claim", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE public.lab_jobs SET status='processing' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := store.Published(ctx, second, 1, 11); err != nil {
		t.Fatal(err)
	}
	if pending, age, err := store.OutboxStats(ctx); err != nil || pending != 0 || age != 0 {
		t.Fatalf("published snapshot: pending=%d age=%g err=%v", pending, age, err)
	}
	job, err := store.Get(ctx, id)
	if err != nil || job.Status != "processing" {
		t.Fatal("publisher regressed worker state", err)
	}
	if _, err := store.Claim(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("published event reclaimed", err)
	}
	// Force an outbox INSERT failure in this isolated DB and prove no orphan job.
	if _, err := db.ExecContext(ctx, `ALTER TABLE public.lab_outbox ADD CONSTRAINT lab_test_reject CHECK (false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		if _, err := db.ExecContext(cleanupCtx, `ALTER TABLE public.lab_outbox DROP CONSTRAINT lab_test_reject`); err != nil {
			t.Errorf("constraint cleanup: %v", err)
		}
	}()
	failedKey := key + "-rollback"
	if _, _, err := store.Enqueue(ctx, Request{failedKey, "lab_effect", Payload{1000}}); err == nil {
		t.Fatal("outbox failure unexpectedly accepted")
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM public.lab_jobs WHERE idempotency_key=$1`, failedKey).Scan(&n); err != nil || n != 0 {
		t.Fatalf("orphan jobs=%d err=%v", n, err)
	}
}
