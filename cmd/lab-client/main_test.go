package main

import (
	"net/http"
	"testing"
	"time"
)

func TestHTTPFailureEventIncludesRespondingPod(t *testing.T) {
	t.Parallel()
	response := &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header)}
	response.Header.Set("X-Lab-Pod", "lab-api-test-1")
	event := httpFailureEvent("test-run", response, 7*time.Millisecond)
	if event["run_id"] != "test-run" || event["kind"] != "http" || event["status"] != http.StatusServiceUnavailable || event["pod"] != "lab-api-test-1" || event["latency_ms"] != int64(7) {
		t.Fatalf("failure event = %#v", event)
	}
	if _, ok := event["time"].(time.Time); !ok {
		t.Fatalf("failure event time = %#v, want time.Time", event["time"])
	}
}
