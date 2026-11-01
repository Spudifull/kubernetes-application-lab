package main

import (
	"context"
	"database/sql"
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
	"github.com/Spudifull/kubernetes-application-lab/internal/tracing"
)

var (
	version   = "dev"
	revision  = "unknown"
	buildDate = "unknown"
)

const (
	listenAddress          = ":8080"
	readinessDrainDuration = 3 * time.Second
	shutdownTimeout        = 10 * time.Second
)

func main() {
	logger := tracing.Logger(slog.New(slog.NewJSONHandler(os.Stdout, nil)), "lab-api")
	if err := run(logger); err != nil {
		logger.Error("application_stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	if len(os.Args) > 1 && os.Args[1] == "enqueue-scheduled" {
		if len(os.Args) != 2 {
			return errors.New("usage: lab-api enqueue-scheduled")
		}
		return enqueueScheduled(logger)
	}
	var db *sql.DB
	if os.Getenv("LAB_DB_ENABLED") == "true" || len(os.Args) > 1 {
		config, err := database.ConfigFromEnv()
		if err != nil {
			return err
		}
		db, err = database.Open(config)
		if err != nil {
			return err
		}
		defer db.Close()
	}
	if len(os.Args) > 1 {
		if os.Args[1] != "migrate" || len(os.Args) > 3 || (len(os.Args) == 3 && os.Args[2] != "--fail-lab") {
			return errors.New("usage: lab-api migrate [--fail-lab]")
		}
		if err := database.Migrate(context.Background(), db, len(os.Args) == 3); err != nil {
			return err
		}
		logger.Info("migration_completed")
		return nil
	}
	stopTracing, err := tracing.Init("lab-api", version, revision, logger)
	if err != nil {
		return err
	}
	defer stopTracing()
	var jobStore app.JobStore
	if os.Getenv("LAB_JOBS_ENABLED") == "true" {
		if db == nil {
			return errors.New("LAB_JOBS_ENABLED requires LAB_DB_ENABLED")
		}
		jobStore = jobs.Store{DB: db}
	}
	podName := os.Getenv("LAB_POD_NAME")
	if podName == "" {
		podName = "unknown"
	}
	application := app.NewWithOptions(app.BuildInfo{
		Version:   version,
		Revision:  revision,
		BuildDate: buildDate,
	}, app.Options{
		EnableExperiments: os.Getenv("LAB_ENABLE_EXPERIMENTS") == "true",
		Logger:            logger,
		PodName:           podName,
		Database:          db,
		Jobs:              jobStore,
	})
	server := app.NewHTTPServer(listenAddress, application.Handler())
	server.ErrorLog = slog.NewLogLogger(logger.Handler(), slog.LevelError)

	terminationSignals := make(chan os.Signal, 1)
	signal.Notify(terminationSignals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(terminationSignals)

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("http_server_started", "address", listenAddress, "version", version, "revision", revision, "pod", podName)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case receivedSignal := <-terminationSignals:
		logger.Info("signal_received", "signal", receivedSignal.String(), "pod", podName)
	}

	application.BeginShutdown()
	logger.Info("shutdown_started", "readiness", false, "drain_duration", readinessDrainDuration, "pod", podName)

	drainTimer := time.NewTimer(readinessDrainDuration)
	<-drainTimer.C

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()
	logger.Info("http_server_shutdown_started", "timeout", shutdownTimeout, "pod", podName)
	if err := server.Shutdown(shutdownContext); err != nil {
		_ = server.Close()
		return err
	}

	if err := <-serverErrors; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	logger.Info("shutdown_completed", "pod", podName)
	return nil
}
