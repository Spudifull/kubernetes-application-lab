package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
)

type fakeJobs struct {
	err        error
	calls      int
	readyError error
}

func (s *fakeJobs) Ready(context.Context) error { return s.readyError }
func (s *fakeJobs) Enqueue(context.Context, jobs.Request) (jobs.Job, bool, error) {
	s.calls++
	return jobs.Job{ID: "00000000-0000-4000-8000-000000000001", Status: "accepted"}, true, s.err
}
func (s *fakeJobs) Get(context.Context, string) (jobs.Job, error) { return jobs.Job{}, s.err }

func TestJobHTTPBoundary(t *testing.T) {
	body := `{"idempotency_key":"one","job_type":"lab_effect","payload":{"duration_ms":1000}}`
	for _, tt := range []struct {
		name, body, media string
		storeError        error
		status, calls     int
	}{
		{"accepted", body, "application/json", nil, 202, 1},
		{"conflict", body, "application/json", jobs.ErrConflict, 409, 1},
		{"database", body, "application/json", errors.New("secret must not escape"), 503, 1},
		{"media", body, "text/plain", nil, 415, 0},
		{"unknown", `{"extra":1}`, "application/json", nil, 400, 0},
		{"trailing", body + ` {}`, "application/json", nil, 400, 0},
		{"invalid duration", strings.Replace(body, "1000", "9000", 1), "application/json", nil, 400, 0},
		{"size", `{"idempotency_key":"` + strings.Repeat("x", 2100) + `"}`, "application/json", nil, 413, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeJobs{err: tt.storeError}
			a := NewWithOptions(BuildInfo{}, Options{Jobs: store, PodName: "pod-one"})
			r := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", tt.media)
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			if w.Code != tt.status || store.calls != tt.calls {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, store.calls, w.Body)
			}
			if strings.Contains(w.Body.String(), "secret") {
				t.Fatal("internal error exposed")
			}
			if w.Header().Get("X-Lab-Pod") != "pod-one" {
				t.Fatal("missing Pod correlation")
			}
		})
	}
}

func TestJobOptInAndShutdown(t *testing.T) {
	a := New(BuildInfo{})
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/jobs", nil))
	if w.Code != 404 {
		t.Fatalf("stateless jobs enabled: %d", w.Code)
	}
	store := &fakeJobs{readyError: errors.New("schema missing")}
	a = NewWithOptions(BuildInfo{}, Options{Jobs: store})
	for _, tt := range []struct {
		path   string
		status int
	}{{"/livez", 200}, {"/readyz", 503}} {
		w = httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest("GET", tt.path, nil))
		if w.Code != tt.status {
			t.Fatalf("%s=%d", tt.path, w.Code)
		}
	}
	a.BeginShutdown()
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/jobs", nil))
	if w.Code != 503 || store.calls != 0 {
		t.Fatal("shutdown admitted a job")
	}
}

func TestJobGetErrors(t *testing.T) {
	for _, tt := range []struct {
		id     string
		err    error
		status int
	}{
		{"bad", nil, 400}, {"00000000-0000-4000-8000-000000000001", sql.ErrNoRows, 404},
		{"00000000-0000-4000-8000-000000000001", errors.New("db"), 503},
	} {
		a := NewWithOptions(BuildInfo{}, Options{Jobs: &fakeJobs{err: tt.err}})
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/jobs/"+tt.id, nil))
		if w.Code != tt.status {
			t.Fatalf("status=%d expected=%d", w.Code, tt.status)
		}
	}
}
