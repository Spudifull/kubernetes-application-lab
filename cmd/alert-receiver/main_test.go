package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func testWebhook(status string) map[string]any {
	return map[string]any{"version": "4", "receiver": "lab-webhook", "status": status,
		"alerts": []any{map[string]any{"status": status, "labels": map[string]string{
			"alertname": "OutboxBacklogGrowing", "namespace": "kubernetes-application-lab", "severity": "warning", "component": "publisher", "secret": "do-not-retain"},
			"annotations": map[string]string{"secret": "do-not-retain"}, "startsAt": "2026-11-01T12:00:00Z", "endsAt": "2026-11-01T12:05:00Z", "fingerprint": "0123456789abcdef"}}}
}

func TestReceiverFiltersAndBoundsEvidence(t *testing.T) {
	s := &sink{logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), startedAt: time.Now().UTC()}
	h := s.handler()
	for i := range 130 {
		status := "firing"
		if i == 129 {
			status = "resolved"
		}
		b, err := json.Marshal(testWebhook(status))
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/alerts", bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusAccepted {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/notifications", nil))
	if strings.Contains(w.Body.String(), "do-not-retain") || len(s.rows) != 128 || s.evicted != 2 || s.rows[127].Status != "resolved" {
		t.Fatal("receiver leaked arbitrary fields or failed to bound its store")
	}
}

func TestReceiverRejectsInvalidWebhooksAtomically(t *testing.T) {
	for _, data := range []string{`{}`, `{} {}`, `{"padding":"` + strings.Repeat("x", 65<<10) + `"}`} {
		s := &sink{logger: slog.Default()}
		r := httptest.NewRequest(http.MethodPost, "/alerts", strings.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, r)
		if w.Code < 400 || len(s.rows) != 0 {
			t.Fatal("invalid request accepted")
		}
	}
	for _, edit := range []func(map[string]any){
		func(b map[string]any) { b["receiver"] = "external" },
		func(b map[string]any) { b["alerts"].([]any)[0].(map[string]any)["fingerprint"] = "invalid" },
		func(b map[string]any) {
			b["alerts"].([]any)[0].(map[string]any)["labels"].(map[string]string)["namespace"] = "other"
		},
	} {
		body := testWebhook("firing")
		edit(body)
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		s := &sink{logger: slog.Default()}
		r := httptest.NewRequest(http.MethodPost, "/alerts", bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest || len(s.rows) != 0 {
			t.Fatal("foreign identity accepted")
		}
	}
}

func TestReceiverConcurrentReadersAndWriters(t *testing.T) {
	s := &sink{logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), startedAt: time.Now().UTC()}
	h := s.handler()
	body, err := json.Marshal(testWebhook("firing"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				r := httptest.NewRequest(http.MethodPost, "/alerts", bytes.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != http.StatusAccepted {
					t.Errorf("write status=%d", w.Code)
					return
				}
				w = httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/notifications", nil))
				if w.Code != http.StatusOK || !json.Valid(w.Body.Bytes()) {
					t.Error("invalid concurrent snapshot")
					return
				}
			}
		})
	}
	wg.Wait()
	if len(s.rows) != 128 || s.evicted != 32 {
		t.Fatalf("lost concurrent writes: rows=%d evicted=%d", len(s.rows), s.evicted)
	}
}
