package app

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/telemetry"
)

type metricKey struct {
	method string
	route  string
	status int
}

type metricValue struct {
	count         uint64
	durationNanos uint64
	latency       telemetry.Distribution
}

// Metrics is a deliberately small Prometheus text exporter. Every label is
// selected from a bounded set; raw URLs and request identifiers are excluded.
type Metrics struct {
	buildInfo BuildInfo
	active    atomic.Int64
	mu        sync.Mutex
	requests  map[metricKey]metricValue
}

func NewMetrics(buildInfo BuildInfo) *Metrics {
	return &Metrics{
		buildInfo: buildInfo,
		requests:  make(map[metricKey]metricValue),
	}
}

func (m *Metrics) Observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.active.Add(1)
		defer m.active.Add(-1)

		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)

		key := metricKey{
			method: boundedMethod(r.Method),
			route:  boundedRoute(r.Pattern),
			status: recorder.status,
		}
		m.mu.Lock()
		value := m.requests[key]
		value.count++
		elapsed := time.Since(started)
		value.durationNanos += uint64(elapsed)
		value.latency.Observe(elapsed)
		m.requests[key] = value
		m.mu.Unlock()
	})
}

func (m *Metrics) Handle(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	type sample struct {
		key   metricKey
		value metricValue
	}

	m.mu.Lock()
	samples := make([]sample, 0, len(m.requests))
	for key, value := range m.requests {
		samples = append(samples, sample{key: key, value: value})
	}
	m.mu.Unlock()

	sort.Slice(samples, func(i, j int) bool {
		left := samples[i].key.method + samples[i].key.route + strconv.Itoa(samples[i].key.status)
		right := samples[j].key.method + samples[j].key.route + strconv.Itoa(samples[j].key.status)
		return left < right
	})

	_, _ = fmt.Fprintln(w, "# HELP lab_http_requests_total Total HTTP requests handled by the application.")
	_, _ = fmt.Fprintln(w, "# TYPE lab_http_requests_total counter")
	for _, sample := range samples {
		labels := labelsFor(sample.key)
		_, _ = fmt.Fprintf(w, "lab_http_requests_total%s %d\n", labels, sample.value.count)
	}

	_, _ = fmt.Fprintln(w, "# HELP lab_http_request_duration_seconds HTTP request duration without quantiles.")
	_, _ = fmt.Fprintln(w, "# TYPE lab_http_request_duration_seconds summary")
	for _, sample := range samples {
		labels := labelsFor(sample.key)
		seconds := float64(sample.value.durationNanos) / float64(time.Second)
		_, _ = fmt.Fprintf(w, "lab_http_request_duration_seconds_sum%s %.9f\n", labels, seconds)
		_, _ = fmt.Fprintf(w, "lab_http_request_duration_seconds_count%s %d\n", labels, sample.value.count)
	}

	// Preserve the existing summary contract; quantiles use this new histogram.
	_, _ = fmt.Fprintln(w, "# HELP lab_http_request_latency_seconds HTTP latency with fixed cumulative buckets.")
	_, _ = fmt.Fprintln(w, "# TYPE lab_http_request_latency_seconds histogram")
	for _, sample := range samples {
		sample.value.latency.WriteSamples(w, "lab_http_request_latency_seconds", labelsFor(sample.key))
	}

	_, _ = fmt.Fprintln(w, "# HELP lab_http_requests_active Current number of HTTP requests.")
	_, _ = fmt.Fprintln(w, "# TYPE lab_http_requests_active gauge")
	_, _ = fmt.Fprintf(w, "lab_http_requests_active %d\n", m.active.Load())
	_, _ = fmt.Fprintln(w, "# HELP lab_build_info Build information for the running application.")
	_, _ = fmt.Fprintln(w, "# TYPE lab_build_info gauge")
	_, _ = fmt.Fprintf(
		w,
		"lab_build_info{version=\"%s\",revision=\"%s\",build_date=\"%s\"} 1\n",
		escapeLabel(m.buildInfo.Version),
		escapeLabel(m.buildInfo.Revision),
		escapeLabel(m.buildInfo.BuildDate),
	)
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(data []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(data)
}

func boundedMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return method
	default:
		return "OTHER"
	}
}

func boundedRoute(pattern string) string {
	if pattern == "" {
		return "unmatched"
	}
	return pattern
}

func labelsFor(key metricKey) string {
	return fmt.Sprintf(
		"{method=\"%s\",route=\"%s\",status=\"%s\"}",
		escapeLabel(key.method),
		escapeLabel(key.route),
		strconv.Itoa(key.status),
	)
}

func escapeLabel(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\"", "\\\"")
	return replacer.Replace(value)
}
