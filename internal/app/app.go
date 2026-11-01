package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/database"
)

const (
	maxWorkDuration = 8 * time.Second
	preStopDelay    = 3 * time.Second
)

type Options struct {
	EnableExperiments bool
	Jobs              JobStore
	Database          *sql.DB
	Logger            *slog.Logger
	PodName           string
}

// BuildInfo identifies the binary that is serving requests.
type BuildInfo struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	BuildDate string `json:"build_date"`
}

// App owns the HTTP surface and the readiness state of the process.
type App struct {
	buildInfo    BuildInfo
	metrics      *Metrics
	ready        atomic.Bool
	shuttingDown atomic.Bool
	workSequence atomic.Uint64
	logger       *slog.Logger
	database     *sql.DB
	jobs         JobStore
	podName      string
	workStarted  func() // Used by deterministic admission tests.
	workWait     func(context.Context, time.Duration) bool
	preStopWait  func(time.Duration) // Used by deterministic lifecycle tests.
	cpuSlots     chan struct{}
	cpuCompute   func(context.Context, int) (uint64, error)
	readCPUStats func() (cpuStats, error)
	handler      http.Handler
}

func New(buildInfo BuildInfo) *App {
	return NewWithOptions(buildInfo, Options{})
}

func NewWithOptions(buildInfo BuildInfo, options Options) *App {
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	a := &App{
		buildInfo: buildInfo,
		metrics:   NewMetrics(buildInfo),
		logger:    logger,
		database:  options.Database,
		jobs:      options.Jobs,
		podName:   options.PodName,
	}
	if a.podName == "" {
		a.podName = "unknown"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.handleRoot)
	mux.HandleFunc("GET /livez", a.handleLive)
	mux.HandleFunc("GET /readyz", a.handleReady)
	mux.HandleFunc("GET /metrics", a.metrics.Handle)
	mux.HandleFunc("GET /version", a.handleVersion)
	mux.HandleFunc("GET /_prestop", a.handlePreStop)
	if a.jobs != nil {
		mux.HandleFunc("POST /jobs", a.handleJobEnqueue)
		mux.HandleFunc("GET /jobs/{id}", a.handleJobGet)
	}
	if a.database != nil {
		mux.HandleFunc("POST /records/{key}", a.handleRecordPut)
		mux.HandleFunc("GET /records/{key}", a.handleRecordGet)
	}
	if options.EnableExperiments {
		a.cpuSlots = make(chan struct{}, maxConcurrentCPUWork)
		mux.HandleFunc("GET /work", a.handleWork)
		mux.HandleFunc("GET /cpu", a.handleCPU)
		mux.HandleFunc("GET /cpu-stat", a.handleCPUStat)
	}

	a.handler = a.withPodHeader(a.metrics.Observe(a.traceJobs(a.rejectNewWorkDuringShutdown(mux))))
	a.ready.Store(true)
	return a
}

func (a *App) handleWork(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()["duration_ms"]
	if len(values) != 1 {
		http.Error(w, "duration_ms must appear exactly once", http.StatusBadRequest)
		return
	}
	milliseconds, err := strconv.Atoi(values[0])
	if err != nil || milliseconds < 1 || milliseconds > int(maxWorkDuration/time.Millisecond) {
		http.Error(w, "duration_ms must be between 1 and 8000", http.StatusBadRequest)
		return
	}

	id := a.workSequence.Add(1)
	runID := r.Header.Get("X-Lab-Run-ID")
	if len(runID) > 64 {
		http.Error(w, "X-Lab-Run-ID exceeds 64 bytes", http.StatusBadRequest)
		return
	}
	a.logger.Info("work_started", "work_id", id, "run_id", runID, "duration_ms", milliseconds)
	if a.workStarted != nil {
		a.workStarted()
	}
	wait := a.workWait
	if wait == nil {
		wait = waitForWork
	}
	if !wait(r.Context(), time.Duration(milliseconds)*time.Millisecond) {
		a.logger.Info("work_canceled", "work_id", id, "run_id", runID, "error", r.Context().Err())
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("work completed\n"))
	a.logger.Info("work_completed", "work_id", id, "run_id", runID)
}

func waitForWork(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}

func (a *App) Handler() http.Handler {
	return a.handler
}

func (a *App) withPodHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Lab-Pod", a.podName)
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		if recorder.status >= http.StatusBadRequest {
			a.logger.Info("http_response_error", "pod", a.podName, "status", recorder.status, "method", r.Method, "path", r.URL.Path)
		}
	})
}

// BeginShutdown makes the process ineligible for new work before the HTTP
// listener is closed. It is safe to call more than once.
func (a *App) BeginShutdown() bool {
	started := a.shuttingDown.CompareAndSwap(false, true)
	a.ready.Store(false)
	return started
}

func (a *App) handleRoot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (a *App) handleLive(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("alive\n"))
}

func (a *App) handleReady(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if !a.ready.Load() {
		a.logger.Info("readiness_rejected", "pod", a.podName)
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	if a.database != nil {
		ready, reason := database.Ready(r.Context(), a.database)
		if !ready {
			a.logger.Info("database_readiness_rejected", "pod", a.podName, "reason", reason)
			http.Error(w, "database unavailable or schema missing", http.StatusServiceUnavailable)
			return
		}
	}

	if a.jobs != nil {
		if err := a.jobs.Ready(r.Context()); err != nil {
			a.logger.Info("jobs_readiness_rejected", "pod", a.podName)
			http.Error(w, "jobs schema unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready\n"))
}

func (a *App) handleVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(a.buildInfo); err != nil {
		// Headers have already been written; the server error log owns reporting
		// connection-level write failures.
		return
	}
}

// handlePreStop keeps the process serving while Kubernetes begins removing the
// terminating Pod from Service routing, before kubelet sends SIGTERM.
func (a *App) handlePreStop(w http.ResponseWriter, _ *http.Request) {
	a.logger.Info("prestop_started", "pod", a.podName, "delay", preStopDelay)
	wait := a.preStopWait
	if wait == nil {
		wait = time.Sleep
	}
	wait(preStopDelay)
	a.logger.Info("prestop_completed", "pod", a.podName)
	w.WriteHeader(http.StatusOK)
}

func (a *App) rejectNewWorkDuringShutdown(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.shuttingDown.Load() && !isDiagnosticPath(r.URL.Path) {
			a.logger.Info("work_rejected_during_shutdown", "pod", a.podName, "method", r.Method, "path", r.URL.Path, "run_id", r.Header.Get("X-Lab-Run-ID"))
			http.Error(w, "shutting down", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isDiagnosticPath(path string) bool {
	switch path {
	case "/livez", "/readyz", "/metrics", "/version":
		return true
	default:
		return false
	}
}
