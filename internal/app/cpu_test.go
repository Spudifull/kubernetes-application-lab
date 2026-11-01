package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestCPURouteIsDisabledByDefault(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/cpu?units=1", "/cpu-stat"} {
		if got := serve(New(BuildInfo{}).Handler(), path).Code; got != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", path, got)
		}
	}
}

func TestCPUParametersAndBoundedMetrics(t *testing.T) {
	t.Parallel()
	application := NewWithOptions(BuildInfo{}, Options{EnableExperiments: true})
	application.cpuCompute = func(_ context.Context, units int) (uint64, error) { return uint64(units), nil }
	for _, path := range []string{"/cpu", "/cpu?units=0", "/cpu?units=101", "/cpu?units=x", "/cpu?units=1&units=2"} {
		if got := serve(application.Handler(), path).Code; got != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", path, got)
		}
	}
	for _, units := range []string{"1", "100"} {
		request := httptest.NewRequest(http.MethodGet, "/cpu?units="+units, nil)
		request.Header.Set("X-Lab-Run-ID", "cpu-test-run")
		response := httptest.NewRecorder()
		application.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"units":`+units) {
			t.Errorf("valid units %s: status=%d body=%s", units, response.Code, response.Body.String())
		}
	}
	metrics := serve(application.Handler(), "/metrics").Body.String()
	if strings.Contains(metrics, "cpu-test-run") || strings.Contains(metrics, "units=100") {
		t.Fatalf("unbounded input leaked into labels:\n%s", metrics)
	}
	if !strings.Contains(metrics, `route="GET /cpu",status="200"`) {
		t.Fatalf("CPU route missing in bounded metrics:\n%s", metrics)
	}
}

func TestCPUConcurrencyLimitAndAcceptedShutdownWork(t *testing.T) {
	t.Parallel()
	application := NewWithOptions(BuildInfo{}, Options{EnableExperiments: true})
	started := make(chan struct{}, maxConcurrentCPUWork)
	release := make(chan struct{})
	application.cpuCompute = func(ctx context.Context, _ int) (uint64, error) {
		started <- struct{}{}
		select {
		case <-release:
			return 42, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	responses := make([]*httptest.ResponseRecorder, maxConcurrentCPUWork)
	var done sync.WaitGroup
	done.Add(maxConcurrentCPUWork)
	for i := range responses {
		responses[i] = httptest.NewRecorder()
		go func(response *httptest.ResponseRecorder) {
			defer done.Done()
			application.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/cpu?units=1", nil))
		}(responses[i])
	}
	for range responses {
		<-started
	}
	if got := serve(application.Handler(), "/cpu?units=1").Code; got != http.StatusTooManyRequests {
		t.Fatalf("third concurrent request = %d, want 429", got)
	}
	application.BeginShutdown()
	if got := serve(application.Handler(), "/cpu?units=1").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("new work after shutdown = %d, want 503", got)
	}
	close(release)
	done.Wait()
	for i, response := range responses {
		if response.Code != http.StatusOK {
			t.Errorf("accepted request %d = %d, want 200", i, response.Code)
		}
	}
}

func TestCPUStopsAfterContextCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := computeCPU(ctx, maxCPUUnits); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled computation = %v, want context.Canceled", err)
	}
	application := NewWithOptions(BuildInfo{}, Options{EnableExperiments: true})
	started := make(chan struct{})
	stopped := make(chan struct{})
	application.cpuCompute = func(ctx context.Context, _ int) (uint64, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return 0, ctx.Err()
	}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/cpu?units=1", nil).WithContext(requestCtx)
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		application.Handler().ServeHTTP(httptest.NewRecorder(), request)
	}()
	<-started
	cancelRequest()
	<-stopped
	done.Wait()
	if len(application.cpuSlots) != 0 {
		t.Fatal("CPU slot was not released after cancellation")
	}
}

func TestCPUStatVersionsAndReadFailure(t *testing.T) {
	t.Parallel()
	v2, err := parseCPUStatV2("usage_usec 5000\nnr_periods 10\nnr_throttled 3\nthrottled_usec 700\n", "10000 100000\n")
	if err != nil || v2.CgroupVersion != 2 || v2.Throttled != 3 || v2.ThrottledUsec != 700 || v2.QuotaUsec == nil || *v2.QuotaUsec != 10000 {
		t.Fatalf("v2 stats = %#v, err=%v", v2, err)
	}
	v1, err := parseCPUStatV1("nr_periods 10\nnr_throttled 4\nthrottled_time 900000\n", "-1", "100000")
	if err != nil || v1.CgroupVersion != 1 || v1.ThrottledUsec != 900 || v1.QuotaUsec != nil {
		t.Fatalf("v1 stats = %#v, err=%v", v1, err)
	}
	if _, err := parseCPUStatV2("nr_periods 10\n", "max 100000"); err == nil {
		t.Fatal("missing throttling counter was accepted")
	}
	var logs bytes.Buffer
	application := NewWithOptions(BuildInfo{}, Options{EnableExperiments: true, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	application.readCPUStats = func() (cpuStats, error) { return cpuStats{}, errors.New("test read failure") }
	if got := serve(application.Handler(), "/cpu-stat").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("failed cgroup read = %d, want 503", got)
	}
	if !strings.Contains(logs.String(), `"msg":"cpu_stat_unavailable"`) {
		t.Fatalf("missing read error log: %s", logs.String())
	}
	application.readCPUStats = func() (cpuStats, error) { return v2, nil }
	response := serve(application.Handler(), "/cpu-stat")
	if response.Code != http.StatusOK {
		t.Fatalf("cgroup status = %d, want 200", response.Code)
	}
	var got cpuStats
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || got.Throttled != v2.Throttled {
		t.Fatalf("cgroup response = %#v, err=%v", got, err)
	}
}
