package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
)

const loggingRelease = "lab-logging"

func loggingOwnership(data []byte) error {
	var releases []struct{ Name, Namespace, Chart, Status string }
	if err := json.Unmarshal(data, &releases); err != nil {
		return err
	}
	for _, r := range releases {
		if r.Name == loggingRelease || strings.HasPrefix(r.Chart, loggingRelease+"-") {
			if r.Name != loggingRelease || r.Namespace != observabilityNS || r.Chart != "lab-logging-0.1.0" ||
				(r.Status != "deployed" && r.Status != "failed") {
				return errors.New("logging release owner/version/state requires review")
			}
		}
	}
	return nil
}

func (e monitoringEvidence) logging(ctx context.Context, action, key string) error {
	if action == "logging-query" || action == "logging-verify" || action == "logging-retry-verify" {
		return e.loggingQuery(ctx, action, key)
	}
	if action == "logging-status" {
		_, helmErr := e.capture(ctx, "logging-status", "helm", "status", loggingRelease, "-n", observabilityNS, "--kube-context", e.lab.context)
		_, kubeErr := e.kube(ctx, "logging-workloads", "get", "deployment/lab-alloy", "statefulset/lab-loki", "service/lab-loki", "-n", observabilityNS, "-o", "json")
		_, pvcErr := e.kube(ctx, "logging-storage", "get", "pvc/data-lab-loki-0", "-n", observabilityNS, "-o", "json")
		_, readyErr := e.proxy(ctx, "loki-ready", "lab-loki:3100", "/ready")
		return errors.Join(helmErr, kubeErr, pvcErr, readyErr)
	}
	if action != "logging-render" && action != "logging-install" {
		return errors.New("unsupported logging action")
	}
	args := []string{"template", loggingRelease, "helm/lab-logging", "--namespace", observabilityNS, "--kube-version", "1.34.0"}
	out, err := e.capture(ctx, "logging-render", "helm", args...)
	if err != nil {
		return err
	}
	manifest := filepath.Join(e.dir, "lab-logging.yaml")
	if err := e.save("lab-logging.yaml", []byte(out)); err != nil {
		return err
	}
	if action == "logging-render" {
		e.lab.print("Review lab-logging.yaml: images, retained PVC, resources, API reader RBAC and index labels. No resources changed.")
		return nil
	}
	if err := e.capacity(ctx); err != nil {
		return err
	}
	if _, err := e.kube(ctx, "logging-namespace", "get", "namespace", observabilityNS); err != nil {
		return err
	}
	if _, err := e.kube(ctx, "logging-crd", "wait", "--for=condition=Established", "crd/podmonitors.monitoring.coreos.com", "--timeout=20s"); err != nil {
		return err
	}
	storage, err := e.kube(ctx, "logging-storage-class", "get", "storageclass", "standard", "-o", "json")
	if err != nil {
		return err
	}
	var sc struct{ Provisioner, ReclaimPolicy, VolumeBindingMode string }
	if json.Unmarshal([]byte(storage), &sc) != nil || sc.Provisioner != "rancher.io/local-path" || sc.ReclaimPolicy != "Delete" || sc.VolumeBindingMode != "WaitForFirstConsumer" {
		return errors.New("review logging storage class before creating PVC")
	}
	releases, err := e.capture(ctx, "logging-ownership", "helm", monitoringReleaseListArgs(e.lab.context)...)
	if err != nil {
		return err
	}
	if err := loggingOwnership([]byte(releases)); err != nil {
		return err
	}
	nodes, err := e.kube(ctx, "logging-capacity-nodes", "get", "nodes", "-o", "json")
	if err != nil {
		return err
	}
	pods, err := e.kube(ctx, "logging-capacity-pods", "get", "pods", "-A", "-o", "json")
	if err != nil {
		return err
	}
	// Same incremental budget as the tracing pair: 640Mi requests / 1280Mi
	// limits, plus 128Mi allowance. All existing Pods stay in the calculation.
	if err := tracingCapacity([]byte(nodes), []byte(pods)); err != nil {
		return fmt.Errorf("logging capacity: %w", err)
	}
	mem, err := e.capture(ctx, "logging-host-memory", "docker", "exec", "kubernetes-application-lab-control-plane", "cat", "/proc/meminfo")
	if err != nil {
		return err
	}
	if err := tracingAvailableMemory(mem); err != nil {
		return fmt.Errorf("logging memory reserve: %w", err)
	}
	if _, err := e.kube(ctx, "logging-dry-run", "apply", "--dry-run=server", "-f", manifest); err != nil {
		return err
	}
	e.lab.print("Installing Loki/Alloy only. Retained 2Gi Loki PVC; application images, database and Kafka are unchanged. Failed resources are retained.")
	bounded, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	if err := e.helmInstall(bounded, "logging-install", "upgrade", "--install", loggingRelease, "helm/lab-logging", "--namespace", observabilityNS,
		"--kube-context", e.lab.context, "--reset-values", "--wait", "--timeout", "180s"); err != nil {
		return err
	}
	return e.logging(ctx, "logging-status", "")
}

type loggingRow struct {
	Time    time.Time `json:"time"`
	Event   string    `json:"event"`
	Service string    `json:"service"`
	Pod     string    `json:"pod"`
	TraceID string    `json:"trace_id"`
	SpanID  string    `json:"span_id"`
	JobID   string    `json:"job_id"`
	EventID string    `json:"event_id"`
}

// Raw line filtering preserves only actual stream labels in Loki's response.
// Parsed JSON fields are checked locally; they never become index labels.
func loggingRows(data []byte, jobID string) ([]loggingRow, error) {
	var response struct {
		Status string
		Data   struct {
			ResultType string
			Result     []struct {
				Stream map[string]string
				Values [][]string
			}
		}
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	if response.Status != "success" || response.Data.ResultType != "streams" {
		return nil, errors.New("Loki did not return successful log streams")
	}
	var rows []loggingRow
	count := 0
	for _, stream := range response.Data.Result {
		for key := range stream.Stream {
			switch key {
			case "namespace", "service", "pod", "container":
			default:
				return nil, fmt.Errorf("unexpected indexed log label %q", key)
			}
		}
		if stream.Stream["namespace"] != mainNS || stream.Stream["pod"] == "" || stream.Stream["container"] == "" {
			return nil, errors.New("Loki stream identity missing or outside application namespace")
		}
		for _, value := range stream.Values {
			count++
			if len(value) != 2 {
				return nil, errors.New("invalid Loki log entry")
			}
			var row loggingRow
			if err := json.Unmarshal([]byte(value[1]), &row); err != nil {
				return nil, errors.New("matching log entry is not application JSON")
			}
			if row.JobID != jobID {
				continue // UUID may appear in another field; that is not correlation.
			}
			if row.Time.IsZero() || row.Time.Location() != time.UTC || row.Event == "" || row.Service != stream.Stream["service"] || row.Pod != stream.Stream["pod"] {
				return nil, errors.New("application log timestamp/event/identity mismatch")
			}
			rows = append(rows, row)
		}
	}
	if count >= 500 {
		return nil, errors.New("log query reached 500-entry limit; inspect saved response, do not infer completeness")
	}
	return rows, nil
}

func loggingCorrelation(rows []loggingRow, retry bool) error {
	required := map[string]string{
		"job_accepted": "lab-api", "outbox_broker_ack": "lab-publisher",
		"worker_received": "lab-worker", "worker_effect_committed": "lab-worker", "worker_offset_committed": "lab-worker",
	}
	traceID, eventID := "", ""
	seen := map[string]bool{}
	retries := map[string]bool{}
	for _, row := range rows {
		service, wanted := required[row.Event]
		if !wanted && row.Event != "worker_retry_scheduled" {
			continue
		}
		if !wanted {
			service = "lab-worker"
		}
		traceBytes, err := hex.DecodeString(row.TraceID)
		spanBytes, spanErr := hex.DecodeString(row.SpanID)
		if row.Service != service || err != nil || len(traceBytes) != 16 || row.TraceID == strings.Repeat("0", 32) ||
			spanErr != nil || len(spanBytes) != 8 || row.SpanID == strings.Repeat("0", 16) {
			return errors.New("correlated event has invalid service or trace/span identity; enable diagnostic tracing")
		}
		if traceID != "" && traceID != row.TraceID {
			return errors.New("job events span different trace IDs; inspect raw evidence")
		}
		traceID = row.TraceID
		if row.Event != "job_accepted" {
			if !jobs.ValidID(row.EventID) || (eventID != "" && eventID != row.EventID) {
				return errors.New("publisher/worker event IDs do not match")
			}
			eventID = row.EventID
		}
		seen[row.Event] = true
		if row.Event == "worker_retry_scheduled" {
			// A repeated collector delivery of the same line is not a new retry.
			retries[row.Time.Format(time.RFC3339Nano)+row.SpanID] = true
		}
	}
	for event := range required {
		if !seen[event] {
			return fmt.Errorf("event %s not found; allow ingestion time then repeat within 30 minutes", event)
		}
	}
	if retry && len(retries) < 2 {
		return errors.New("two distinct controlled retry events not found")
	}
	return nil
}

func (e monitoringEvidence) loggingQuery(ctx context.Context, action, key string) error {
	if key == "" {
		b, err := os.ReadFile(".local-kafka/job-locator.json")
		if err != nil {
			return err
		}
		var job jobs.Job
		if err := json.Unmarshal(b, &job); err != nil {
			return err
		}
		key = job.ID
	}
	if !jobs.ValidID(key) {
		return errors.New("KEY must be a job UUID")
	}
	now := time.Now().UTC()
	query := `{namespace="` + mainNS + `",service=~"lab-api|lab-publisher|lab-worker"} |= "` + key + `"`
	params := url.Values{"query": {query}, "start": {strconv.FormatInt(now.Add(-30*time.Minute).UnixNano(), 10)},
		"end": {strconv.FormatInt(now.UnixNano(), 10)}, "limit": {"500"}, "direction": {"forward"}}
	out, err := e.proxy(ctx, "loki-job-query", "lab-loki:3100", "/loki/api/v1/query_range?"+params.Encode())
	if err != nil {
		return err
	}
	rows, err := loggingRows([]byte(out), key)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := e.lab.event("logging_observation", map[string]any{"log": row}); err != nil {
			return err
		}
	}
	if len(rows) == 0 {
		return errors.New("no matching application logs in the last 30 minutes")
	}
	if action != "logging-query" {
		if err := loggingCorrelation(rows, action == "logging-retry-verify"); err != nil {
			return err
		}
	}
	return e.lab.event("logging_query_completed", map[string]any{"job_id": key, "rows": len(rows), "check": action,
		"scope": "returned application logs only; not exactly-once log delivery or database verification"})
}
