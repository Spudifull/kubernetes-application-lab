package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
	"github.com/Spudifull/kubernetes-application-lab/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// JobStore is the HTTP boundary; the real implementation commits before returning.
type JobStore interface {
	Ready(context.Context) error
	Enqueue(context.Context, jobs.Request) (jobs.Job, bool, error)
	Get(context.Context, string) (jobs.Job, error)
}

func (a *App) handleJobEnqueue(w http.ResponseWriter, r *http.Request) {
	_, validation := tracing.Start(r.Context(), "jobs.validate", trace.SpanKindInternal)
	validated := false
	defer func() {
		if !validated {
			validation.End()
		}
	}()
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request jobs.Request
	if err := decoder.Decode(&request); err != nil {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			http.Error(w, "job body too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "invalid job JSON", http.StatusBadRequest)
		}
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			http.Error(w, "job body too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "one JSON object required", http.StatusBadRequest)
		}
		return
	}
	if err := request.Validate(); err != nil {
		http.Error(w, "invalid key, job_type or duration_ms (1..8000)", http.StatusBadRequest)
		return
	}
	validation.End()
	validated = true
	// Enqueue is a sibling of validation under the bounded HTTP span.
	job, created, err := a.jobs.Enqueue(r.Context(), request)
	if errors.Is(err, jobs.ErrConflict) {
		http.Error(w, "idempotency key has different content", http.StatusConflict)
		return
	}
	if err != nil {
		a.logger.WarnContext(r.Context(), "job_enqueue_failed", "pod", a.podName, "reason", "database_unavailable")
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	trace.SpanFromContext(r.Context()).SetAttributes(attribute.String("job.id", job.ID), attribute.Bool("lab.job.created", created))
	a.logger.InfoContext(r.Context(), "job_accepted", "pod", a.podName, "job_id", job.ID, "created", created)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", "/jobs/"+job.ID)
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(job) // Connection write failures are owned by HTTP server.
}

func (a *App) handleJobGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !jobs.ValidID(id) {
		http.Error(w, "invalid job ID", http.StatusBadRequest)
		return
	}
	job, err := a.jobs.Get(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(job)
}
