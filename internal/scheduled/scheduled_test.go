package scheduled

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLogicalRunIdentity(t *testing.T) {
	request, err := Request("lab", "lab-cron", "2026-10-26T00:00:00Z")
	if err != nil || request.Validate() != nil {
		t.Fatalf("invalid request: %+v %v", request, err)
	}
	equivalent, err := Request("lab", "lab-cron", "2026-10-26T03:00:00+03:00")
	if err != nil || equivalent != request {
		t.Fatal("equivalent scheduled instants must select the same key and payload")
	}
	for _, tc := range []struct{ namespace, name, timestamp string }{
		{"other", "lab-cron", "2026-10-26T00:00:00Z"},
		{"lab", "other", "2026-10-26T00:00:00Z"},
		{"lab", "lab-cron", "2026-10-26T00:01:00Z"},
	} {
		next, err := Request(tc.namespace, tc.name, tc.timestamp)
		if err != nil || next.IdempotencyKey == request.IdempotencyKey {
			t.Fatalf("different logical run shared identity: %+v %v", tc, err)
		}
	}
}

func TestInvalidLogicalRuns(t *testing.T) {
	for _, tc := range []struct{ namespace, name, timestamp string }{
		{"", "lab-cron", "2026-10-26T00:00:00Z"},
		{"lab", "../jobs", "2026-10-26T00:00:00Z"},
		{"lab", "lab-cron", ""},
		{"lab", "lab-cron", "now"},
		{"lab", "lab-cron", "0001-01-01T00:00:00Z"},
		{"lab", "lab-cron", "2026-10-26T00:00:00.1Z"},
	} {
		if _, err := Request(tc.namespace, tc.name, tc.timestamp); err == nil {
			t.Fatalf("accepted invalid logical run: %+v", tc)
		}
	}
}

func TestParentJobTimestamp(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		status                      int
		owner, timestamp, namespace string
		wantError                   bool
	}{
		{"scheduled", 200, "lab-cron", "2026-10-26T00:00:00Z", "lab", false},
		{"missing annotation", 200, "lab-cron", "", "lab", true},
		{"wrong owner", 200, "other", "2026-10-26T00:00:00Z", "lab", true},
		{"wrong namespace", 200, "lab-cron", "2026-10-26T00:00:00Z", "other", true},
		{"forbidden", 403, "lab-cron", "2026-10-26T00:00:00Z", "lab", true},
		{"missing job", 404, "lab-cron", "2026-10-26T00:00:00Z", "lab", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/apis/batch/v1/namespaces/lab/jobs/run-1" || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("unexpected parent Job request")
				}
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]any{
					"name": "run-1", "namespace": tc.namespace,
					"annotations":     map[string]string{TimestampAnnotation: tc.timestamp},
					"ownerReferences": []any{map[string]any{"apiVersion": "batch/v1", "kind": "CronJob", "name": tc.owner, "controller": true}},
				}})
			}))
			defer server.Close()
			got, err := ScheduledTimestamp(context.Background(), server.Client(), server.URL, "test-token", "lab", "run-1", "lab-cron")
			if (err != nil) != tc.wantError || !tc.wantError && got != tc.timestamp {
				t.Fatalf("timestamp=%q error=%v", got, err)
			}
		})
	}
}

func TestCanceledParentJobRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ScheduledTimestamp(ctx, &http.Client{}, "http://127.0.0.1:1", "unused", "lab", "run-1", "lab-cron"); err == nil {
		t.Fatal("canceled read must not supply a timestamp")
	}
}
