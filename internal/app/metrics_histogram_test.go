package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPHistogramPreservesSummaryAndBoundsRoutes(t *testing.T) {
	m := NewMetrics(BuildInfo{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /jobs/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	h := m.Observe(mux)
	for _, path := range []string{"/jobs/private-job-1?token=secret", "/jobs/private-job-2", "/unknown-private-url"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}
	w := httptest.NewRecorder()
	m.Handle(w, httptest.NewRequest("GET", "/metrics", nil))
	text := w.Body.String()
	for _, want := range []string{
		`lab_http_request_duration_seconds_count{method="GET",route="GET /jobs/{id}",status="200"} 2`,
		`lab_http_request_latency_seconds_bucket{method="GET",route="GET /jobs/{id}",status="200",le="+Inf"} 2`,
		`lab_http_request_latency_seconds_count{method="GET",route="unmatched",status="404"} 1`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s: %s", want, text)
		}
	}
	if strings.Count(text, "# TYPE lab_http_request_latency_seconds histogram") != 1 {
		t.Fatal("duplicate histogram TYPE")
	}
	for _, forbidden := range []string{"private-job", "secret", "unknown-private-url"} {
		if strings.Contains(text, forbidden) {
			t.Fatal("raw URL leaked into metric")
		}
	}
}
