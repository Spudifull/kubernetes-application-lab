package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestJobTracingPreservesMetricsRoute(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous); _ = provider.Shutdown(context.Background()) })
	a := NewWithOptions(BuildInfo{}, Options{Jobs: &fakeJobs{}})
	r := httptest.NewRequest("POST", "/jobs?secret=must-not-export", strings.NewReader(`{"idempotency_key":"secret-key","job_type":"lab_effect","payload":{"duration_ms":1000}}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 202 || w.Header().Get("X-Lab-Trace-ID") != "0123456789abcdef0123456789abcdef" {
		t.Fatal("correlation missing", w.Code, w.Header())
	}
	for _, path := range []string{"/livez", "/metrics", "/readyz"} {
		a.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}
	spans := recorder.Ended()
	if len(spans) != 2 {
		t.Fatalf("unexpected spans including probes: %d", len(spans))
	}
	for _, s := range spans {
		for _, a := range s.Attributes() {
			if strings.Contains(a.Value.Emit(), "secret") {
				t.Fatal("private HTTP data exported")
			}
		}
	}
	metrics := httptest.NewRecorder()
	a.Handler().ServeHTTP(metrics, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `route="POST /jobs",status="202"`) {
		t.Fatal("HTTP metrics route lost", metrics.Body.String())
	}
}
