// alert-receiver is an internal, bounded diagnostic sink, not a notification service.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"sync"
	"syscall"
	"time"
)

var revision = "unknown"
var fingerprintPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

var acceptedAlerts = map[string]bool{
	"APIUnavailable": true, "OutboxBacklogGrowing": true, "KafkaConsumerLagHigh": true,
	"WorkerProcessingStalled": true, "DLQMessagesDetected": true, "PostgreSQLUnavailable": true,
}

type webhookAlert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	StartsAt    time.Time         `json:"startsAt"`
	EndsAt      time.Time         `json:"endsAt"`
	Fingerprint string            `json:"fingerprint"`
}

type notification struct {
	ReceivedAt  time.Time         `json:"received_at"`
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	StartsAt    time.Time         `json:"starts_at"`
	EndsAt      time.Time         `json:"ends_at"`
	Fingerprint string            `json:"fingerprint"`
}

type sink struct {
	mu        sync.Mutex
	rows      []notification
	evicted   uint64
	startedAt time.Time
	logger    *slog.Logger
}

func (s *sink) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /alerts", s.receive)
	mux.HandleFunc("GET /notifications", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		rows := append([]notification{}, s.rows...)
		evicted := s.evicted
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(struct {
			StartedAt time.Time      `json:"started_at"`
			Evicted   uint64         `json:"evicted"`
			Rows      []notification `json:"notifications"`
		}{s.startedAt, evicted, rows}); err != nil {
			s.logger.Warn("receiver_response_failed")
		}
	})
	return mux
}

func (s *sink) receive(w http.ResponseWriter, r *http.Request) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	defer r.Body.Close()
	// Alertmanager may add fields. Decode only the diagnostic contract and never
	// retain annotations, URLs, arbitrary labels or the original webhook body.
	var body struct {
		Version  string         `json:"version"`
		Receiver string         `json:"receiver"`
		Status   string         `json:"status"`
		Alerts   []webhookAlert `json:"alerts"`
	}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&body); err != nil {
		http.Error(w, "invalid or oversized webhook", http.StatusBadRequest)
		return
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) || body.Version != "4" || body.Receiver != "lab-webhook" ||
		(body.Status != "firing" && body.Status != "resolved") || len(body.Alerts) < 1 || len(body.Alerts) > 20 {
		http.Error(w, "unsupported webhook", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	rows := make([]notification, 0, len(body.Alerts))
	for _, alert := range body.Alerts {
		component := alert.Labels["component"]
		if !acceptedAlerts[alert.Labels["alertname"]] || alert.Labels["namespace"] != "kubernetes-application-lab" || alert.Labels["severity"] != "warning" ||
			(component != "api" && component != "publisher" && component != "worker" && component != "kafka" && component != "postgres") ||
			(alert.Status != "firing" && alert.Status != "resolved") || alert.StartsAt.IsZero() ||
			!fingerprintPattern.MatchString(alert.Fingerprint) || (alert.Status == "resolved" && (alert.EndsAt.IsZero() || alert.EndsAt.Before(alert.StartsAt))) {
			http.Error(w, "unexpected laboratory alert", http.StatusBadRequest)
			return
		}
		labels := make(map[string]string)
		for _, key := range []string{"alertname", "namespace", "severity", "component"} {
			value := alert.Labels[key]
			if len(value) > 128 {
				http.Error(w, "label too long", http.StatusBadRequest)
				return
			}
			labels[key] = value
		}
		rows = append(rows, notification{now, alert.Status, labels, alert.StartsAt, alert.EndsAt, alert.Fingerprint})
	}
	s.mu.Lock()
	s.rows = append(s.rows, rows...)
	if len(s.rows) > 128 {
		s.evicted += uint64(len(s.rows) - 128)
		s.rows = append([]notification(nil), s.rows[len(s.rows)-128:]...)
	}
	s.mu.Unlock()
	for _, row := range rows {
		s.logger.Info("alert_notification_received", "status", row.Status, "alertname", row.Labels["alertname"], "fingerprint", row.Fingerprint,
			"starts_at", row.StartsAt, "ends_at", row.EndsAt, "received_at", row.ReceivedAt)
	}
	w.WriteHeader(http.StatusAccepted)
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			a.Value = slog.TimeValue(a.Value.Time().UTC())
		}
		return a
	}}))
	s := &sink{startedAt: time.Now().UTC(), logger: logger}
	server := &http.Server{Addr: ":8085", Handler: s.handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 5 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	logger.Info("alert_receiver_started", "revision", revision)
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("alert_receiver_failed")
			os.Exit(1)
		}
	case <-ctx.Done():
		bounded, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := server.Shutdown(bounded); err != nil {
			logger.Error("alert_receiver_shutdown_failed")
			os.Exit(1)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			logger.Error("alert_receiver_failed")
			os.Exit(1)
		}
	}
}
