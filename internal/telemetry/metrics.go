package telemetry

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type Build struct{ Version, Revision, Date string }

// Metrics has a fixed registration set per component. Unknown names are ignored
// rather than creating unbounded series from arbitrary errors or identifiers.
type Metrics struct {
	mu        sync.Mutex
	component string
	build     Build
	counters  map[string]uint64
	active    int64
	duration  Distribution
	lastAck   float64
	outbox    struct {
		pending int64
		age     float64
		at      time.Time
		up      bool
	}
}

func NewPublisher(build Build) *Metrics {
	return newMetrics("publisher", build, []string{"attempts", "publish_success", "publish_errors", "retries", "outbox_published", "outbox_mark_errors", "claim_errors", "outbox_stats_errors"})
}
func NewWorker(build Build) *Metrics {
	return newMetrics("worker", build, []string{"jobs_processed", "jobs_failed", "jobs_retried", "jobs_duplicates", "jobs_dlq", "kafka_offset_commit_failures", "kafka_offset_commits"})
}
func newMetrics(component string, build Build, names []string) *Metrics {
	m := &Metrics{component: component, build: build, counters: make(map[string]uint64, len(names))}
	for _, n := range names {
		m.counters[n] = 0
	}
	return m
}
func (m *Metrics) Add(name string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.counters[name]; ok {
		m.counters[name]++
	}
}
func (m *Metrics) Begin() func() {
	if m == nil {
		return func() {}
	}
	start := time.Now()
	m.mu.Lock()
	m.active++
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.active--
		m.duration.Observe(time.Since(start))
	}
}
func (m *Metrics) Ack() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastAck = float64(time.Now().UnixNano()) / 1e9
}

// PollOutbox owns one bounded, sequential SQL measurement every ten seconds.
// The caller cancels ctx and waits for this function before closing the DB.
func (m *Metrics) PollOutbox(ctx context.Context, read func(context.Context) (int64, float64, error)) {
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
		bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
		pending, age, err := read(bounded)
		cancel()
		m.setOutbox(pending, age, err, time.Now())
		timer.Reset(10 * time.Second)
	}
}
func (m *Metrics) setOutbox(pending int64, age float64, err error, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil || pending < 0 || age < 0 || math.IsNaN(age) || math.IsInf(age, 0) {
		m.outbox.up = false
		m.counters["outbox_stats_errors"]++
		return
	}
	m.outbox.pending, m.outbox.age, m.outbox.at, m.outbox.up = pending, age, at, true
}

func (m *Metrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	// Copy under a short lock; a slow HTTP reader never blocks processing.
	m.mu.Lock()
	counters := make(map[string]uint64, len(m.counters))
	for n, v := range m.counters {
		counters[n] = v
	}
	active, duration, ack, snapshot := m.active, m.duration, m.lastAck, m.outbox
	m.mu.Unlock()
	names := make([]string, 0, len(counters))
	for n := range counters {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		name := "lab_" + m.component + "_" + n + "_total"
		_, _ = fmt.Fprintf(w, "# TYPE %s counter\n%s %d\n", name, name, counters[n])
	}
	name := "lab_" + m.component + "_active"
	_, _ = fmt.Fprintf(w, "# TYPE %s gauge\n%s %d\n", name, name, active)
	name = "lab_" + m.component + "_attempt_duration_seconds"
	duration.Write(w, name, "")
	_, _ = fmt.Fprintf(w, "# TYPE lab_build_info gauge\nlab_build_info{version=%s,revision=%s,build_date=%s} 1\n", quoteLabel(m.build.Version), quoteLabel(m.build.Revision), quoteLabel(m.build.Date))
	if m.component != "publisher" {
		return
	}
	_, _ = fmt.Fprintf(w, "# TYPE lab_publisher_last_broker_ack_timestamp_seconds gauge\nlab_publisher_last_broker_ack_timestamp_seconds %.6f\n", ack)
	up, pending, age, timestamp := 0, math.NaN(), math.NaN(), float64(0)
	if snapshot.up {
		up = 1
	}
	if !snapshot.at.IsZero() {
		pending, age = float64(snapshot.pending), snapshot.age
		timestamp = float64(snapshot.at.UnixNano()) / 1e9
	}
	for _, s := range []struct {
		name  string
		value float64
	}{
		{"lab_outbox_pending_events", pending}, {"lab_outbox_oldest_pending_age_seconds", age},
		{"lab_outbox_stats_up", float64(up)}, {"lab_outbox_stats_last_success_timestamp_seconds", timestamp},
	} {
		_, _ = fmt.Fprintf(w, "# TYPE %s gauge\n%s %g\n", s.name, s.name, s.value)
	}
}

func quoteLabel(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`).Replace(value) + `"`
}
