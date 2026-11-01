package app

import (
	"net/http"
	"strings"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
	"github.com/Spudifull/kubernetes-application-lab/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// A deliberately small HTTP boundary keeps raw URL/query/headers out of spans.
// Health checks and metric scrapes never start traces.
func (a *App) traceJobs(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := ""
		if r.Method == http.MethodPost && r.URL.Path == "/jobs" {
			route = "POST /jobs"
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/jobs/") && jobs.ValidID(strings.TrimPrefix(r.URL.Path, "/jobs/")) {
			route = "GET /jobs/{id}"
		}
		if a.jobs == nil || route == "" {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		parents := r.Header.Values("traceparent")
		if len(parents) == 1 {
			ctx = tracing.Extract(ctx, parents[0])
		}
		ctx, span := tracing.Start(ctx, route, trace.SpanKindServer,
			attribute.String("http.route", strings.TrimPrefix(route, r.Method+" ")), attribute.String("http.request.method", r.Method))
		defer span.End()
		if parent := tracing.Parent(ctx); parent != "" {
			w.Header().Set("traceparent", parent)
			w.Header().Set("X-Lab-Trace-ID", trace.SpanContextFromContext(ctx).TraceID().String())
		}
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		tracedRequest := r.WithContext(ctx)
		next.ServeHTTP(recorder, tracedRequest)
		// Preserve ServeMux route selection for the outer metrics middleware.
		r.Pattern = tracedRequest.Pattern
		span.SetAttributes(attribute.Int("http.response.status_code", recorder.status))
		if recorder.status >= 500 {
			span.SetStatus(codes.Error, "http_server_error")
		}
	})
}
