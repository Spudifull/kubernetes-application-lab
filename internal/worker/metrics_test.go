package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
	"github.com/Spudifull/kubernetes-application-lab/internal/telemetry"
)

func metricsText(m *telemetry.Metrics) string {
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	return w.Body.String()
}

func TestMetricsCountSQLSuccessBeforeOffsetAndDuplicatesSeparately(t *testing.T) {
	for _, tc := range []struct {
		name                                  string
		dbErr, commitErr                      error
		duplicate                             bool
		processed, duplicates, commitFailures string
	}{
		{"success", nil, nil, false, "1", "0", "0"},
		{"SQL failed", errors.New("sql"), nil, false, "0", "0", "0"},
		{"commit failed after SQL", nil, errors.New("commit"), false, "1", "0", "1"},
		{"duplicate", nil, nil, true, "0", "1", "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := []string{}
			m := telemetry.NewWorker(telemetry.Build{})
			p := Processor{Store: testStore{calls: &calls, completeErr: tc.dbErr, duplicate: tc.duplicate}, Committer: testCommitter{&calls, tc.commitErr}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Work: func(context.Context, time.Duration) error { return nil }, Metrics: m}
			_ = p.Handle(context.Background(), testRecord())
			text := metricsText(m)
			for _, want := range []string{"lab_worker_jobs_processed_total " + tc.processed + "\n", "lab_worker_jobs_duplicates_total " + tc.duplicates + "\n", "lab_worker_kafka_offset_commit_failures_total " + tc.commitFailures + "\n", "lab_worker_active 0\n"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q: %s", want, text)
				}
			}
		})
	}
}

func TestRetryWaitAndRecordedDLQMetrics(t *testing.T) {
	calls := []string{}
	m := telemetry.NewWorker(telemetry.Build{})
	s := &reliabilityTestStore{calls: &calls, state: jobs.RetryState{FailuresBeforeSuccess: 3}}
	p := Processor{Store: testStore{calls: &calls}, Committer: testCommitter{calls: &calls}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Work: func(context.Context, time.Duration) error { return nil }, Metrics: m, Reliability: &Reliability{Store: s, Publisher: reliabilityTestPublisher{calls: &calls}}}
	if !errors.Is(p.Handle(context.Background(), testRecord()), ErrRetryPending) {
		t.Fatal("retry not scheduled")
	}
	s.state.Wait = time.Second
	_ = p.Handle(context.Background(), testRecord()) // Waiting is not another persisted retry.
	s.state = jobs.RetryState{Failures: 3, FailuresBeforeSuccess: 3}
	if err := p.Handle(context.Background(), testRecord()); err != nil {
		t.Fatal(err)
	}
	if err := p.Handle(context.Background(), testRecord()); err != nil {
		t.Fatal(err)
	} // Existing receipt is not a new DLQ publication.
	text := metricsText(m)
	for _, want := range []string{"lab_worker_jobs_retried_total 1\n", "lab_worker_jobs_failed_total 1\n", "lab_worker_jobs_dlq_total 1\n", "lab_worker_jobs_processed_total 0\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
}
