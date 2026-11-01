package app

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEndpoints(t *testing.T) {
	t.Parallel()

	buildInfo := BuildInfo{Version: "1.2.3", Revision: "abc123", BuildDate: "2026-10-07T00:00:00Z"}
	application := New(buildInfo)

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "root", path: "/", wantStatus: http.StatusOK, wantBody: "ok\n"},
		{name: "live", path: "/livez", wantStatus: http.StatusOK, wantBody: "alive\n"},
		{name: "ready", path: "/readyz", wantStatus: http.StatusOK, wantBody: "ready\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := serve(application.Handler(), test.path)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if response.Body.String() != test.wantBody {
				t.Fatalf("body = %q, want %q", response.Body.String(), test.wantBody)
			}
		})
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()

	want := BuildInfo{Version: "1.2.3", Revision: "abc123", BuildDate: "2026-10-07T00:00:00Z"}
	response := serve(New(want).Handler(), "/version")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}

	var got BuildInfo
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got != want {
		t.Fatalf("version = %#v, want %#v", got, want)
	}
}

func TestBeginShutdownChangesAdmissionState(t *testing.T) {
	t.Parallel()

	application := New(BuildInfo{})
	beforeShutdown := serve(application.Handler(), "/")
	if beforeShutdown.Code != http.StatusOK {
		t.Fatalf("application status before shutdown = %d, want %d", beforeShutdown.Code, http.StatusOK)
	}

	if !application.BeginShutdown() {
		t.Fatal("first BeginShutdown call did not start shutdown")
	}
	if application.BeginShutdown() {
		t.Fatal("second BeginShutdown call started shutdown again")
	}

	root := serve(application.Handler(), "/")
	if root.Code != http.StatusServiceUnavailable {
		t.Fatalf("root status = %d, want %d", root.Code, http.StatusServiceUnavailable)
	}

	diagnostics := []struct {
		path       string
		wantStatus int
	}{
		{path: "/livez", wantStatus: http.StatusOK},
		{path: "/readyz", wantStatus: http.StatusServiceUnavailable},
		{path: "/metrics", wantStatus: http.StatusOK},
		{path: "/version", wantStatus: http.StatusOK},
	}
	for _, diagnostic := range diagnostics {
		response := serve(application.Handler(), diagnostic.path)
		if response.Code != diagnostic.wantStatus {
			t.Errorf("%s status = %d, want %d", diagnostic.path, response.Code, diagnostic.wantStatus)
		}
	}
}

func TestPreStopKeepsServingUntilSignal(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	application := NewWithOptions(BuildInfo{}, Options{
		Logger:  slog.New(slog.NewJSONHandler(&logs, nil)),
		PodName: "lab-api-test-1",
	})
	application.preStopWait = func(delay time.Duration) {
		if delay != preStopDelay {
			t.Errorf("preStop delay = %s, want %s", delay, preStopDelay)
		}
		for _, path := range []string{"/", "/readyz"} {
			if response := serve(application.Handler(), path); response.Code != http.StatusOK {
				t.Errorf("%s during preStop = %d, want 200", path, response.Code)
			}
		}
	}

	response := serve(application.Handler(), "/_prestop")
	if response.Code != http.StatusOK || response.Header().Get("X-Lab-Pod") != "lab-api-test-1" {
		t.Fatalf("preStop response = %d, pod = %q", response.Code, response.Header().Get("X-Lab-Pod"))
	}
	for _, event := range []string{`"msg":"prestop_started"`, `"msg":"prestop_completed"`} {
		if !strings.Contains(logs.String(), event) {
			t.Errorf("missing %s in logs: %s", event, logs.String())
		}
	}

	application.BeginShutdown()
	for _, path := range []string{"/", "/readyz"} {
		if response := serve(application.Handler(), path); response.Code != http.StatusServiceUnavailable {
			t.Errorf("%s after shutdown = %d, want 503", path, response.Code)
		}
	}
}

func TestWorkAcceptedDuringPreStopCompletesAfterShutdown(t *testing.T) {
	t.Parallel()

	application := NewWithOptions(BuildInfo{}, Options{EnableExperiments: true})
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	response := httptest.NewRecorder()
	application.workStarted = func() { close(started) }
	application.workWait = func(ctx context.Context, _ time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-release:
			return true
		}
	}
	application.preStopWait = func(_ time.Duration) {
		go func() {
			defer close(finished)
			application.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/work?duration_ms=8000", nil))
		}()
		<-started
	}

	if got := serve(application.Handler(), "/_prestop").Code; got != http.StatusOK {
		t.Fatalf("preStop status = %d, want 200", got)
	}
	application.BeginShutdown()
	if got := serve(application.Handler(), "/work?duration_ms=1").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("new work after shutdown = %d, want 503", got)
	}
	close(release)
	<-finished
	if response.Code != http.StatusOK {
		t.Fatalf("work accepted during preStop = %d, want 200", response.Code)
	}
}

func TestPodHeaderAndShutdownRejectionLog(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	application := NewWithOptions(BuildInfo{}, Options{EnableExperiments: true, Logger: logger, PodName: "lab-api-test-1"})

	for _, path := range []string{"/", "/readyz", "/work?duration_ms=bad", "/missing"} {
		response := serve(application.Handler(), path)
		if got := response.Header().Get("X-Lab-Pod"); got != "lab-api-test-1" {
			t.Errorf("%s pod header = %q, want lab-api-test-1", path, got)
		}
	}

	application.BeginShutdown()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Lab-Run-ID", "test-run")
	response := httptest.NewRecorder()
	application.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("X-Lab-Pod") != "lab-api-test-1" {
		t.Fatalf("shutdown response = %d, pod = %q", response.Code, response.Header().Get("X-Lab-Pod"))
	}
	ready := serve(application.Handler(), "/readyz")
	if ready.Code != http.StatusServiceUnavailable || ready.Header().Get("X-Lab-Pod") != "lab-api-test-1" {
		t.Fatalf("readiness response = %d, pod = %q", ready.Code, ready.Header().Get("X-Lab-Pod"))
	}
	for _, fragment := range []string{`"msg":"work_rejected_during_shutdown"`, `"pod":"lab-api-test-1"`, `"run_id":"test-run"`, `"msg":"readiness_rejected"`, `"msg":"http_response_error"`, `"status":400`, `"status":404`} {
		if !strings.Contains(logs.String(), fragment) {
			t.Errorf("log does not contain %q:\n%s", fragment, logs.String())
		}
	}
	if metrics := serve(application.Handler(), "/metrics").Body.String(); strings.Contains(metrics, "lab-api-test-1") || strings.Contains(metrics, "test-run") {
		t.Fatalf("pod name or run ID leaked into metrics:\n%s", metrics)
	}
}

func TestBeginShutdownIsConcurrentAndIdempotent(t *testing.T) {
	t.Parallel()

	const callers = 32
	application := New(BuildInfo{})
	start := make(chan struct{})
	results := make(chan bool, callers)

	var callersDone sync.WaitGroup
	callersDone.Add(callers)
	for range callers {
		go func() {
			defer callersDone.Done()
			<-start
			results <- application.BeginShutdown()
		}()
	}

	close(start)
	callersDone.Wait()
	close(results)

	started := 0
	for result := range results {
		if result {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("BeginShutdown reported start %d times, want 1", started)
	}
}

func TestUnknownRouteIsNotFoundAndRecordedAsUnmatched(t *testing.T) {
	t.Parallel()

	application := New(BuildInfo{})
	response := serve(application.Handler(), "/does-not-exist")
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown route status = %d, want %d", response.Code, http.StatusNotFound)
	}

	metrics := serve(application.Handler(), "/metrics")
	if !strings.Contains(metrics.Body.String(), `route="unmatched",status="404"} 1`) {
		t.Fatalf("unknown route metric is missing:\n%s", metrics.Body.String())
	}
}

func TestMetricsExposeBoundedRequestAndBuildSignals(t *testing.T) {
	t.Parallel()

	application := New(BuildInfo{Version: "test", Revision: "deadbeef", BuildDate: "known"})
	_ = serve(application.Handler(), "/livez")
	_ = serve(application.Handler(), "/does-not-exist")
	response := serve(application.Handler(), "/metrics")

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	for _, fragment := range []string{
		"lab_http_requests_total",
		"route=\"GET /livez\"",
		"route=\"unmatched\"",
		"lab_http_request_duration_seconds_sum",
		"lab_http_requests_active 1",
		"lab_build_info{version=\"test\",revision=\"deadbeef\",build_date=\"known\"} 1",
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("metrics body does not contain %q:\n%s", fragment, body)
		}
	}
}

func TestHTTPServerHasBoundedTimeouts(t *testing.T) {
	t.Parallel()

	server := NewHTTPServer(":0", http.NotFoundHandler())
	if server.ReadHeaderTimeout <= 0 || server.ReadTimeout <= 0 || server.WriteTimeout <= 0 || server.IdleTimeout <= 0 {
		t.Fatalf("server timeouts must be positive: %#v", server)
	}
	if server.MaxHeaderBytes <= 0 {
		t.Fatalf("MaxHeaderBytes = %d, want positive value", server.MaxHeaderBytes)
	}
	if server.WriteTimeout <= maxWorkDuration {
		t.Fatalf("WriteTimeout = %s must exceed max work duration %s", server.WriteTimeout, maxWorkDuration)
	}
}

func TestWorkDisabledByDefault(t *testing.T) {
	t.Parallel()
	response := serve(New(BuildInfo{}).Handler(), "/work?duration_ms=1")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

func TestWorkValidationAndMetrics(t *testing.T) {
	t.Parallel()
	a := NewWithOptions(BuildInfo{}, Options{EnableExperiments: true})
	for _, path := range []string{
		"/work", "/work?duration_ms=0", "/work?duration_ms=8001",
		"/work?duration_ms=-1", "/work?duration_ms=9223372036855",
		"/work?duration_ms=9999999999999999999999999999999999",
		"/work?duration_ms=not-a-number", "/work?duration_ms=1&duration_ms=2",
	} {
		if response := serve(a.Handler(), path); response.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", path, response.Code)
		}
	}

	response := serve(a.Handler(), "/work?duration_ms=1")
	if response.Code != http.StatusOK || response.Body.String() != "work completed\n" {
		t.Fatalf("work response = %d %q", response.Code, response.Body.String())
	}
	metrics := serve(a.Handler(), "/metrics").Body.String()
	if !strings.Contains(metrics, `route="GET /work",status="200"`) || strings.Contains(metrics, "duration_ms") {
		t.Fatalf("work metric is missing or has an unbounded label:\n%s", metrics)
	}
	a.workWait = func(_ context.Context, duration time.Duration) bool {
		if duration != maxWorkDuration {
			t.Errorf("duration = %s, want %s", duration, maxWorkDuration)
		}
		return true
	}
	if response := serve(a.Handler(), "/work?duration_ms=8000"); response.Code != http.StatusOK {
		t.Fatalf("upper boundary status = %d, want 200", response.Code)
	}
}

func TestAcceptedWorkCompletesAfterBeginShutdown(t *testing.T) {
	t.Parallel()
	a := NewWithOptions(BuildInfo{}, Options{EnableExperiments: true})
	started := make(chan struct{})
	release := make(chan struct{})
	a.workStarted = func() { close(started) }
	a.workWait = func(ctx context.Context, _ time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-release:
			return true
		}
	}
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/work?duration_ms=5000", nil))
	}()
	<-started
	a.BeginShutdown()
	if got := serve(a.Handler(), "/work?duration_ms=1").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("new work status = %d, want 503", got)
	}
	if got := serve(a.Handler(), "/readyz").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want 503", got)
	}
	close(release)
	<-done
	if response.Code != http.StatusOK {
		t.Fatalf("accepted work status = %d, want 200", response.Code)
	}
}

func TestWorkStopsOnClientCancellation(t *testing.T) {
	t.Parallel()
	a := NewWithOptions(BuildInfo{}, Options{EnableExperiments: true})
	started := make(chan struct{})
	a.workStarted = func() { close(started) }
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/work?duration_ms=8000", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Handler().ServeHTTP(response, request)
	}()
	<-started
	cancel()
	<-done
	if response.Body.Len() != 0 {
		t.Fatalf("canceled work returned a body: %q", response.Body.String())
	}
}

func serve(handler http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
