package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/database"
	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
	"github.com/Spudifull/kubernetes-application-lab/internal/scheduled"
)

func enqueueScheduled(logger *slog.Logger) error {
	hold, err := scheduled.HoldDuration(os.Getenv("LAB_CRON_HOLD_SECONDS"))
	if err != nil {
		return err
	}
	signals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signals, hold+30*time.Second)
	defer cancel()
	namespace, cronName := os.Getenv("LAB_NAMESPACE"), os.Getenv("LAB_CRON_NAME")
	// An explicit timestamp is for reproducible manual replay. It is empty in
	// the CronJob template; ordinary scheduled runs must read their parent Job.
	timestamp := os.Getenv("LAB_CRON_SCHEDULED_AT")
	if timestamp == "" {
		ca, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
		if err != nil {
			return errors.New("Kubernetes CA unavailable")
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(ca) {
			return errors.New("invalid Kubernetes CA")
		}
		token, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
		if err != nil {
			return errors.New("Kubernetes token unavailable")
		}
		transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		timestamp, err = scheduled.ScheduledTimestamp(ctx, client, "https://kubernetes.default.svc", strings.TrimSpace(string(token)), namespace, os.Getenv("LAB_JOB_NAME"), cronName)
		if err != nil {
			return err
		}
	}
	request, err := scheduled.Request(namespace, cronName, timestamp)
	if err != nil {
		return err
	}
	logger.Info("scheduled_job_prepared", "scheduled_at", timestamp, "idempotency_key", request.IdempotencyKey, "hold_duration", hold)
	if err := scheduled.Hold(ctx, hold); err != nil {
		logger.Info("scheduled_job_canceled_before_sql", "scheduled_at", timestamp, "idempotency_key", request.IdempotencyKey)
		return err
	}
	config, err := database.ConfigFromEnv()
	if err != nil {
		return err
	}
	db, err := database.Open(config)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := jobs.Store{DB: db}
	if err := store.Ready(ctx); err != nil {
		return errors.New("scheduled jobs schema unavailable")
	}
	job, created, err := store.Enqueue(ctx, request)
	if err != nil {
		// Avoid logging database connection details or credentials.
		return errors.New("scheduled enqueue failed; replay the same timestamp after recovery")
	}
	logger.Info("scheduled_job_enqueued", "namespace", namespace, "cronjob", cronName, "scheduled_at", timestamp,
		"idempotency_key", request.IdempotencyKey, "job_id", job.ID, "created", created, "status", job.Status,
		"version", version, "revision", revision)
	return nil
}
