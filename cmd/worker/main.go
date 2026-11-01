package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/app"
	"github.com/Spudifull/kubernetes-application-lab/internal/database"
	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
	"github.com/Spudifull/kubernetes-application-lab/internal/kafka"
	"github.com/Spudifull/kubernetes-application-lab/internal/telemetry"
	"github.com/Spudifull/kubernetes-application-lab/internal/tracing"
	"github.com/Spudifull/kubernetes-application-lab/internal/worker"
	"github.com/twmb/franz-go/pkg/kgo"
)

var version, revision, buildDate = "dev", "unknown", "unknown"

type session struct {
	gate   *worker.Gate
	client *kgo.Client
}

func main() {
	logger := tracing.Logger(slog.New(slog.NewJSONHandler(os.Stdout, nil)), "lab-worker").With("pod", os.Getenv("LAB_POD_NAME"))
	if err := run(logger); err != nil {
		logger.Error("worker_stopped", "reason", err)
		os.Exit(1)
	}
}
func run(logger *slog.Logger) error {
	if len(os.Args) > 1 {
		if os.Args[1] == "group-status" && len(os.Args) == 2 {
			return groupStatus()
		}
		if os.Args[1] == "poison" && len(os.Args) == 3 {
			return publishInvalid(os.Args[2])
		}
		valid := (os.Args[1] == "replay" || os.Args[1] == "dlq-read") && len(os.Args) == 3
		valid = valid || (os.Args[1] == "configure-retry" && len(os.Args) == 4)
		if !valid {
			return errors.New("usage: lab-worker [group-status | replay JOB_UUID | configure-retry JOB_UUID FAILURES | poison MODE | dlq-read JOB_UUID_OR_latest-invalid]")
		}
	}
	config, err := database.ConfigFromEnv()
	if err != nil {
		return err
	}
	db, err := database.Open(config)
	if err != nil {
		return errors.New("database configuration invalid")
	}
	defer db.Close()
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "configure-retry":
			failures, err := strconv.Atoi(os.Args[3])
			if err != nil {
				return errors.New("invalid failure count")
			}
			if err := (jobs.Store{DB: db}).ConfigureWorkerFault(context.Background(), os.Args[2], failures); err != nil {
				return err
			}
			return diagnosticEvent("worker_fault_configured", map[string]any{"job_id": os.Args[2], "failures_before_success": failures})
		case "dlq-read":
			return readDeadLetter(db, os.Args[2])
		default:
			return replayEvent(db, os.Args[2])
		}
	}
	stopTracing, err := tracing.Init("lab-worker", version, revision, logger)
	if err != nil {
		return err
	}
	defer stopTracing()
	crashJobID := os.Getenv("LAB_WORKER_CRASH_BEFORE_OFFSET_JOB_ID")
	if crashJobID != "" && !jobs.ValidID(crashJobID) {
		return errors.New("LAB_WORKER_CRASH_BEFORE_OFFSET_JOB_ID must be a job UUID")
	}
	store := jobs.Store{DB: db}
	kafkaConfig := kafka.Config{Bootstrap: os.Getenv("LAB_KAFKA_BOOTSTRAP"), CAFile: os.Getenv("LAB_KAFKA_CA_FILE"), CertFile: os.Getenv("LAB_KAFKA_CERT_FILE"), KeyFile: os.Getenv("LAB_KAFKA_KEY_FILE")}
	if _, err := kafkaConfig.TLS(); err != nil {
		return errors.New("worker Kafka TLS configuration invalid")
	}
	dlqConfig := kafkaConfig
	if bootstrap := os.Getenv("LAB_WORKER_DLQ_BOOTSTRAP"); bootstrap != "" {
		dlqConfig.Bootstrap = bootstrap
	}
	// A separate producer has a shorter budget than the bounded consumer attempt.
	dlqClient, err := kafka.NewClient(dlqConfig, kgo.DialTimeout(time.Second), kgo.RequestTimeoutOverhead(time.Second),
		kgo.ProduceRequestTimeout(time.Second), kgo.RecordDeliveryTimeout(3*time.Second), kgo.MaxBufferedRecords(4))
	if err != nil {
		return errors.New("DLQ producer configuration invalid")
	}
	defer dlqClient.Close()
	metrics := telemetry.NewWorker(telemetry.Build{Version: version, Revision: revision, Date: buildDate})
	policy := &worker.Reliability{Store: store, Publisher: dlqPublisher{dlqClient}, Metrics: metrics}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownObserved := make(chan struct{})
	go func() {
		<-ctx.Done()
		logger.Info("worker_shutdown_requested")
		close(shutdownObserved)
	}()
	var active atomic.Pointer[session]
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics)
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		s := active.Load()
		if ctx.Err() != nil || s == nil || !s.gate.Ready() || store.WorkerReady(r.Context()) != nil {
			http.Error(w, "worker not ready", 503)
			return
		}
		ping, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if s.client.Ping(ping) != nil {
			http.Error(w, "Kafka unavailable", 503)
			return
		}
		w.WriteHeader(200)
	})
	server := app.NewHTTPServer(":8082", mux)
	server.ErrorLog = slog.NewLogLogger(logger.Handler(), slog.LevelError)
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.ListenAndServe(); stop() }()
	logger.Info("worker_started", "version", version, "revision", revision, "build_date", buildDate, "group", worker.Group)
	var terminalErr error
	clientID := os.Getenv("LAB_POD_NAME")
	if clientID == "" {
		clientID = "lab-worker"
	}
	for ctx.Err() == nil {
		if err := store.WorkerReady(ctx); err != nil {
			logger.Warn("worker_schema_unavailable")
			if worker.Wait(ctx, 2*time.Second) != nil {
				break
			}
			continue
		}
		gate := &worker.Gate{}
		clientCtx, cancelClient := context.WithCancel(context.Background())
		opts := append(worker.Options(gate, logger), kgo.WithContext(clientCtx), kgo.ClientID(clientID))
		client, err := kafka.NewClient(kafkaConfig, opts...)
		if err != nil {
			cancelClient()
			terminalErr = errors.New("worker Kafka configuration rejected")
			stop()
			break
		}
		active.Store(&session{gate, client})
		var consumer worker.Consumer = client
		if crashJobID != "" {
			consumer = crashConsumer{Consumer: client, jobID: crashJobID, logger: logger, exit: os.Exit}
		}
		err = worker.Consume(ctx, consumer, store, gate, logger, policy)
		active.Store(nil)
		if ctx.Err() != nil {
			logger.Info("worker_shutdown_started")
		}
		client.AllowRebalance()
		leave, cancelLeave := context.WithTimeout(context.Background(), 2*time.Second)
		if leaveErr := client.LeaveGroupContext(leave); leaveErr != nil {
			logger.Warn("worker_group_leave_incomplete")
		}
		cancelLeave()
		cancelClient() // Bound client teardown if the coordinator cannot reply.
		client.CloseAllowingRebalance()
		if ctx.Err() != nil {
			break
		}
		if errors.Is(err, jobs.ErrEvent) {
			// No silent skip, offset reset or hot poison-message retry loop.
			// DLQ policy is a later iteration. Keep live, unready, and diagnosable.
			logger.Error("worker_blocked_invalid_event", "action", "inspect_source_record_no_offset_committed")
			<-ctx.Done()
			break
		}
		logger.Warn("worker_session_retry", "reason", "processing_or_transport_failed", "retry_delay", 2*time.Second)
		if worker.Wait(ctx, 2*time.Second) != nil {
			break
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		_ = server.Close()
	}
	if err := <-serverDone; !errors.Is(err, http.ErrServerClosed) {
		<-shutdownObserved
		return errors.New("worker health listener failed")
	}
	<-shutdownObserved
	logger.Info("worker_shutdown_completed")
	return terminalErr
}
