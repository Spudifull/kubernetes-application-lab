package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/app"
	"github.com/Spudifull/kubernetes-application-lab/internal/database"
	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
	"github.com/Spudifull/kubernetes-application-lab/internal/kafka"
	"github.com/Spudifull/kubernetes-application-lab/internal/publisher"
	"github.com/Spudifull/kubernetes-application-lab/internal/telemetry"
	"github.com/Spudifull/kubernetes-application-lab/internal/tracing"
)

var version, revision, buildDate = "dev", "unknown", "unknown"

func main() {
	logger := tracing.Logger(slog.New(slog.NewJSONHandler(os.Stdout, nil)), "lab-publisher").With("pod", os.Getenv("LAB_POD_NAME"))
	if err := run(logger); err != nil {
		logger.Error("publisher_stopped", "reason", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	config, err := database.ConfigFromEnv()
	if err != nil {
		return err
	}
	db, err := database.Open(config)
	if err != nil {
		return errors.New("database configuration invalid")
	}
	defer db.Close()
	store := jobs.Store{DB: db}
	client, err := kafka.NewClient(kafka.Config{
		Bootstrap: os.Getenv("LAB_KAFKA_BOOTSTRAP"), CAFile: os.Getenv("LAB_KAFKA_CA_FILE"),
		CertFile: os.Getenv("LAB_KAFKA_CERT_FILE"), KeyFile: os.Getenv("LAB_KAFKA_KEY_FILE"),
	})
	if err != nil {
		return errors.New("Kafka TLS configuration invalid")
	}
	defer client.Close() // All synchronous deliveries have completed before Run returns.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	stopTracing, err := tracing.Init("lab-publisher", version, revision, logger)
	if err != nil {
		return err
	}
	defer stopTracing()
	metrics := telemetry.NewPublisher(telemetry.Build{Version: version, Revision: revision, Date: buildDate})
	snapshotDone := make(chan struct{})
	go func() {
		defer close(snapshotDone)
		metrics.PollOutbox(ctx, store.OutboxStats)
	}()
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics)
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if ctx.Err() != nil || store.Ready(r.Context()) != nil {
			http.Error(w, "outbox unavailable", http.StatusServiceUnavailable)
			return
		}
		pingCtx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if client.Ping(pingCtx) != nil {
			http.Error(w, "Kafka unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	server := app.NewHTTPServer(":8081", mux)
	server.ErrorLog = slog.NewLogLogger(logger.Handler(), slog.LevelError)
	serverDone := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		serverDone <- err
		stop() // A dead health listener must not leave an invisible background process.
	}()
	logger.Info("publisher_started", "version", version, "revision", revision, "build_date", buildDate)
	publisher.Publisher{Store: store, Producer: client, Logger: logger, Metrics: metrics}.Run(ctx)
	stop()
	<-snapshotDone // SQL sampling is canceled and bounded before DB teardown.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
	}
	if err := <-serverDone; !errors.Is(err, http.ErrServerClosed) {
		return errors.New("publisher health listener failed")
	}
	logger.Info("publisher_shutdown_completed")
	return nil
}
