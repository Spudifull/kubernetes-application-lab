package telemetry

import (
	"context"
	"errors"
	"math"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func body(m *Metrics) string {
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	return w.Body.String()
}

func TestDistributionBoundariesAndInfinity(t *testing.T) {
	var d Distribution
	for _, elapsed := range []time.Duration{5 * time.Millisecond, 6 * time.Millisecond, time.Minute} {
		d.Observe(elapsed)
	}
	var b strings.Builder
	d.Write(&b, "lab_test_seconds", `{route="GET /jobs/{id}"}`)
	for _, sample := range []string{
		`lab_test_seconds_bucket{route="GET /jobs/{id}",le="0.005"} 1`,
		`lab_test_seconds_bucket{route="GET /jobs/{id}",le="0.01"} 2`,
		`lab_test_seconds_bucket{route="GET /jobs/{id}",le="+Inf"} 3`,
		`lab_test_seconds_count{route="GET /jobs/{id}"} 3`,
	} {
		if !strings.Contains(b.String(), sample) {
			t.Fatalf("missing %q: %s", sample, b.String())
		}
	}
	if math.Abs(d.Sum-(5*time.Millisecond+6*time.Millisecond+time.Minute).Seconds()) > 1e-9 {
		t.Fatal("incorrect histogram sum")
	}
}

func TestOutboxUnknownAndFailedSamplesAreNotZero(t *testing.T) {
	m := NewPublisher(Build{})
	if text := body(m); !strings.Contains(text, "lab_outbox_pending_events NaN\n") || !strings.Contains(text, "lab_outbox_stats_up 0\n") {
		t.Fatal(text)
	}
	at := time.Unix(100, 0)
	m.setOutbox(7, 12, nil, at)
	m.setOutbox(0, 0, errors.New("password=secret-not-for-metrics"), at.Add(time.Minute))
	text := body(m)
	for _, want := range []string{"lab_outbox_pending_events 7\n", "lab_outbox_oldest_pending_age_seconds 12\n", "lab_outbox_stats_last_success_timestamp_seconds 100\n", "lab_outbox_stats_up 0\n", "lab_publisher_outbox_stats_errors_total 1\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	if strings.Contains(text, "secret-not-for-metrics") {
		t.Fatal("SQL error leaked")
	}
	m.setOutbox(0, 0, nil, at.Add(2*time.Minute))
	if text = body(m); !strings.Contains(text, "lab_outbox_pending_events 0\n") || !strings.Contains(text, "lab_outbox_stats_up 1\n") {
		t.Fatal(text)
	}
}

func TestOutboxSamplerHasDeadlineAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := NewPublisher(Build{})
	called := false
	m.PollOutbox(ctx, func(readCtx context.Context) (int64, float64, error) {
		called = true
		if _, ok := readCtx.Deadline(); !ok {
			t.Error("SQL sampling has no deadline")
		}
		cancel()
		return 2, 3, nil
	})
	if !called || !strings.Contains(body(m), "lab_outbox_pending_events 2\n") {
		t.Fatal("no initial sample")
	}
}

func TestConcurrentRecordingAndScrapingIsBounded(t *testing.T) {
	m := NewWorker(Build{Version: "test", Revision: "known", Date: "known"})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				done := m.Begin()
				m.Add("jobs_processed")
				m.Add("job_id=secret")
				done()
				_ = body(m)
			}
		}()
	}
	wg.Wait()
	text := body(m)
	for _, want := range []string{"lab_worker_jobs_processed_total 400\n", "lab_worker_active 0\n", "lab_worker_attempt_duration_seconds_count 400\n"} {
		if !strings.Contains(text, want) {
			t.Fatal(text)
		}
	}
	for _, forbidden := range []string{"job_id", "event_id", "trace_id", "payload", "idempotency_key", "secret"} {
		if strings.Contains(text, forbidden) {
			t.Fatal("unbounded series: " + text)
		}
	}
}
