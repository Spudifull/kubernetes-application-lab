package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const receiverRepository = "kubernetes-application-lab-alert-receiver"
const receiverRefFile = ".local-observability/alert-receiver-image-ref"

func watchedAlertName(key string) (string, error) {
	if key == "" {
		key = "OutboxBacklogGrowing"
	}
	switch key {
	case "OutboxBacklogGrowing", "KafkaConsumerLagHigh", "WorkerProcessingStalled", "DLQMessagesDetected", "APIUnavailable", "PostgreSQLUnavailable":
		return key, nil
	default:
		return "", errors.New("KEY must name one of the six laboratory alert rules")
	}
}

func alertRecoverySignal(name string) string {
	if name == "PostgreSQLUnavailable" {
		return "known_postgres_replica_deficit_and_connectivity"
	}
	if name == "APIUnavailable" {
		return "known_api_replica_deficit"
	}
	if name == "DLQMessagesDetected" {
		return "observed_dlq_increase_5m"
	}
	if name == "OutboxBacklogGrowing" {
		return "fresh_outbox_pending"
	}
	return "known_consumer_lag"
}

func alertRecoveryQuery(name string) (string, error) {
	if _, err := watchedAlertName(name); err != nil {
		return "", err
	}
	if name == "PostgreSQLUnavailable" {
		ready := `kube_statefulset_status_replicas_ready{namespace="` + mainNS + `",statefulset="lab-postgres"}`
		pg := `pg_up{namespace="` + observabilityNS + `",job="lab-postgres-exporter"}`
		// This recovery gate is scoped to the documented zero-replica fault.
		// A known stopped StatefulSet proves absence even if exporter samples
		// disappear. A zero deficit only proves recovery with pg_up=1 and UP.
		healthy := `(max(` + pg + `) == 1) and (max(up{namespace="` + observabilityNS + `",job="lab-postgres-exporter"}) == 1) and ((time()-max(timestamp(` + pg + `))) < 60)`
		return `(clamp_min(1-max(` + ready + `),0)) and (max(up{namespace="` + observabilityNS + `",job="kube-state-metrics"}) == 1) and ((time()-max(timestamp(` + ready + `))) < 60) and ((max(` + ready + `) == 0) or (` + healthy + `))`, nil
	}
	if name == "APIUnavailable" {
		available := `kube_deployment_status_replicas_available{namespace="` + mainNS + `",deployment="lab-api"}`
		return `(clamp_min(1-max(` + available + `),0)) and (max(up{namespace="` + observabilityNS + `",job="kube-state-metrics"}) == 1) and ((time()-max(timestamp(` + available + `))) < 60)`, nil
	}
	if name == "DLQMessagesDetected" {
		selector := `namespace="` + mainNS + `",job="lab-worker"`
		counter := `lab_worker_jobs_dlq_total{` + selector + `}`
		// Keep instance identity: a replacement worker must not turn a lost
		// process counter into evidence that the observation window aged out.
		return `increase(` + counter + `[5m]) and on(instance) (up{` + selector + `} == 1) and on(instance) (resets(` + counter + `[5m]) == 0)`, nil
	}
	if name == "" || name == "OutboxBacklogGrowing" {
		selector := `namespace="` + mainNS + `",job="lab-publisher"`
		return `max(lab_outbox_pending_events{` + selector + `} and on(instance) (lab_outbox_stats_up{` + selector + `} == 1) and on(instance) ((time()-lab_outbox_stats_last_success_timestamp_seconds{` + selector + `}) < 30) and on(instance) (up{` + selector + `} == 1))`, nil
	}
	lag := `kafka_consumergroup_lag{namespace="` + observabilityNS + `",job="lab-kafka-exporter",consumergroup="lab-workers",topic="lab.jobs.v1"}`
	return `(sum(` + lag + `)) and (count(` + lag + ` >= 0) == 3) and (max(up{namespace="` + observabilityNS + `",job="lab-kafka-exporter"}) == 1)`, nil
}

func (l lab) alertRulesTest(ctx context.Context) error {
	if _, err := exec.LookPath("promtool"); err == nil {
		out, err := l.must(ctx, "promtool", nil, "test", "rules", "helm/lab-alerting/tests/rules.test.json")
		l.print(out)
		return err
	}
	chart, err := filepath.Abs("helm/lab-alerting")
	if err != nil {
		return err
	}
	if strings.Contains(chart, ",") {
		return errors.New("chart path cannot contain comma in a Docker mount")
	}
	// Same Prometheus version as the owner-recorded monitoring installation.
	// Mount only the chart, read-only, with no network or Kubernetes credentials.
	out, err := l.must(ctx, "docker", nil, "run", "--rm", "--network", "none", "--read-only", "--memory", "512m", "--cpus", "1",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--tmpfs", "/tmp:rw,noexec,nosuid,size=256m",
		"--mount", "type=bind,source="+chart+",target=/work,readonly", "--workdir", "/work/tests", "--entrypoint", "/bin/promtool",
		"quay.io/prometheus/prometheus:v3.15.0-distroless", "test", "rules", "rules.test.json")
	l.print(out)
	return err
}

func receiverImageTag(ref string) (string, error) {
	tag, ok := strings.CutPrefix(ref, receiverRepository+":")
	b, err := hex.DecodeString(tag)
	if !ok || err != nil || len(b) != 32 || tag != strings.ToLower(tag) {
		return "", errors.New("receiver image must use a recorded content-identity tag")
	}
	return tag, nil
}

func (l lab) receiverImageRecord(ctx context.Context) error {
	id, err := l.must(ctx, "docker", nil, "image", "inspect", "--format", "{{.Id}}", receiverRepository+":build")
	if err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	ref := receiverRepository + ":" + strings.TrimPrefix(id, "sha256:")
	if _, err := receiverImageTag(ref); err != nil || !strings.HasPrefix(id, "sha256:") {
		return errors.New("invalid receiver image ID")
	}
	if _, err := l.must(ctx, "docker", nil, "image", "tag", receiverRepository+":build", ref); err != nil {
		return err
	}
	if err := os.MkdirAll(".local-observability", 0700); err != nil {
		return err
	}
	if err := os.WriteFile(receiverRefFile, []byte(ref+"\n"), 0600); err != nil {
		return err
	}
	l.print("Recorded alert receiver image: " + ref)
	return nil
}

func alertingOwnership(data []byte) error {
	var releases []struct{ Name, Namespace, Chart, Status string }
	if err := json.Unmarshal(data, &releases); err != nil {
		return err
	}
	for _, r := range releases {
		if r.Name == "lab-alerting" || strings.HasPrefix(r.Chart, "lab-alerting-") {
			if r.Name != "lab-alerting" || r.Namespace != observabilityNS || r.Chart != "lab-alerting-0.1.0" || (r.Status != "deployed" && r.Status != "failed") {
				return errors.New("review alerting release owner/version/state")
			}
		}
	}
	return nil
}

func (e monitoringEvidence) alerting(ctx context.Context, action, key string) error {
	name, err := watchedAlertName(key)
	if err != nil {
		return err
	}
	if action == "alerts-status" {
		_, err := e.alertSnapshot(ctx, "status", name)
		return err
	}
	if action == "alerts-watch" {
		return e.alertWatch(ctx, name)
	}
	if action != "alerts-render" && action != "alerts-install" {
		return errors.New("unsupported alerting action")
	}
	b, err := os.ReadFile(receiverRefFile)
	if err != nil {
		return err
	}
	ref := strings.TrimSpace(string(b))
	tag, err := receiverImageTag(ref)
	if err != nil {
		return err
	}
	id, err := e.capture(ctx, "receiver-image", "docker", "image", "inspect", "--format", "{{.Id}}", ref)
	if err != nil {
		return err
	}
	if strings.TrimSpace(id) != "sha256:"+tag {
		return errors.New("receiver image ID does not match recorded tag")
	}
	args := []string{"template", "lab-alerting", "helm/lab-alerting", "--namespace", observabilityNS, "--kube-version", "1.34.0", "--set-string", "image.tag=" + tag}
	out, err := e.capture(ctx, "alerting-render", "helm", args...)
	if err != nil {
		return err
	}
	if err := e.save("alerting.yaml", []byte(out)); err != nil {
		return err
	}
	if action == "alerts-render" {
		e.lab.print("Review receiver, six Prometheus rules, thresholds, actions and routing values. No resources changed.")
		return nil
	}
	if err := e.capacity(ctx); err != nil {
		return err
	}
	releases, err := e.capture(ctx, "alerting-ownership", "helm", monitoringReleaseListArgs(e.lab.context)...)
	if err != nil {
		return err
	}
	if err := alertingOwnership([]byte(releases)); err != nil {
		return err
	}
	if _, err := e.kube(ctx, "alerting-crd", "wait", "--for=condition=Established", "crd/prometheusrules.monitoring.coreos.com", "--timeout=20s"); err != nil {
		return err
	}
	if _, err := e.kube(ctx, "alerting-dry-run", "apply", "--dry-run=server", "-f", filepath.Join(e.dir, "alerting.yaml")); err != nil {
		return err
	}
	if _, err := e.capture(ctx, "receiver-kind-load", "kind", "load", "docker-image", ref, "--name", "kubernetes-application-lab"); err != nil {
		return err
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := e.helmInstall(bounded, "alerting-install", "upgrade", "--install", "lab-alerting", "helm/lab-alerting", "--namespace", observabilityNS,
		"--kube-context", e.lab.context, "--reset-values", "--set-string", "image.tag="+tag, "--wait", "--timeout", "120s"); err != nil {
		return err
	}
	e.lab.print("Receiver and rules installed; update existing monitoring routing with observability-install, then use alerts-status. Installation is not notification evidence.")
	return nil
}

type alertNotification struct {
	ReceivedAt  time.Time         `json:"received_at"`
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	StartsAt    time.Time         `json:"starts_at"`
	EndsAt      time.Time         `json:"ends_at"`
	Fingerprint string            `json:"fingerprint"`
}

type alertSnapshot struct {
	// nil means missing, stale or failed sampler/scrape, never a known zero.
	RecoveryValue  *float64
	SignalIdentity string
	Rule           struct {
		State, Health  string
		LastEvaluation time.Time
	}
	Receiver struct {
		StartedAt     time.Time           `json:"started_at"`
		Evicted       uint64              `json:"evicted"`
		Notifications []alertNotification `json:"notifications"`
	}
	ActiveFingerprints map[string]bool
}

func (e monitoringEvidence) alertSnapshot(ctx context.Context, label, name string) (alertSnapshot, error) {
	var snapshot alertSnapshot
	data, err := e.proxy(ctx, label+"-rules", "lab-monitoring-prometheus:9090", "/api/v1/rules?type=alert")
	if err != nil {
		return snapshot, err
	}
	var rules struct {
		Status string
		Data   struct {
			Groups []struct {
				Rules []struct {
					Name, State, Health string
					LastEvaluation      time.Time
				}
			}
		}
	}
	if json.Unmarshal([]byte(data), &rules) != nil || rules.Status != "success" {
		return snapshot, errors.New("invalid Prometheus rules response")
	}
	found := false
	for _, group := range rules.Data.Groups {
		for _, rule := range group.Rules {
			if rule.Name == name {
				if found {
					return snapshot, fmt.Errorf("duplicate %s rule", name)
				}
				found = true
				snapshot.Rule.State, snapshot.Rule.Health, snapshot.Rule.LastEvaluation = rule.State, rule.Health, rule.LastEvaluation
			}
		}
	}
	if !found || snapshot.Rule.Health != "ok" || time.Since(snapshot.Rule.LastEvaluation) > time.Minute || snapshot.Rule.LastEvaluation.After(time.Now().Add(5*time.Second)) {
		return snapshot, fmt.Errorf("%s rule missing, unhealthy or stale", name)
	}
	query, err := alertRecoveryQuery(name)
	if err != nil {
		return snapshot, err
	}
	data, err = e.proxy(ctx, label+"-recovery-signal", "lab-monitoring-prometheus:9090", "/api/v1/query?query="+url.QueryEscape(query))
	if err != nil {
		return snapshot, err
	}
	var sample struct {
		Status string
		Data   struct {
			ResultType string
			Result     []struct {
				Value  []json.RawMessage
				Metric map[string]string
			}
		}
	}
	if json.Unmarshal([]byte(data), &sample) != nil || sample.Status != "success" || sample.Data.ResultType != "vector" {
		return snapshot, errors.New("invalid alert recovery query response")
	}
	if len(sample.Data.Result) == 1 && len(sample.Data.Result[0].Value) == 2 {
		var value string
		if json.Unmarshal(sample.Data.Result[0].Value[1], &value) == nil {
			pending, parseErr := strconv.ParseFloat(value, 64)
			if parseErr == nil && !math.IsNaN(pending) && !math.IsInf(pending, 0) && pending >= 0 {
				snapshot.RecoveryValue = &pending
				if name == "DLQMessagesDetected" {
					snapshot.SignalIdentity = sample.Data.Result[0].Metric["instance"]
					if snapshot.SignalIdentity == "" {
						snapshot.RecoveryValue = nil
					}
				}
			}
		}
	}
	data, err = e.proxy(ctx, label+"-alertmanager", "lab-monitoring-alertmanager:9093", "/api/v2/alerts?active=true")
	if err != nil {
		return snapshot, err
	}
	var alerts []struct {
		Fingerprint string
		Labels      map[string]string
		Status      struct{ State string }
	}
	if err := json.Unmarshal([]byte(data), &alerts); err != nil {
		return snapshot, err
	}
	snapshot.ActiveFingerprints = make(map[string]bool)
	for _, alert := range alerts {
		if alert.Labels["alertname"] == name && alert.Labels["namespace"] == mainNS && alert.Status.State == "active" {
			snapshot.ActiveFingerprints[alert.Fingerprint] = true
		}
	}
	data, err = e.proxy(ctx, label+"-receiver", "lab-alert-receiver:8085", "/notifications")
	if err != nil {
		return snapshot, err
	}
	if json.Unmarshal([]byte(data), &snapshot.Receiver) != nil || snapshot.Receiver.StartedAt.IsZero() {
		return snapshot, errors.New("invalid receiver response")
	}
	if err := e.lab.event("alert_observation", map[string]any{"rule_state": snapshot.Rule.State, "rule_health": snapshot.Rule.Health,
		"alertname": name, "recovery_signal": alertRecoverySignal(name), "recovery_value": snapshot.RecoveryValue, "signal_identity": snapshot.SignalIdentity, "active_fingerprints": snapshot.ActiveFingerprints, "receiver": snapshot.Receiver}); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

type alertProof struct {
	AlertName                string
	SignalIdentity           string
	Since, ReceiverStarted   time.Time
	Evicted                  uint64
	Pending, Firing, Manager bool
	Fingerprint              string
	StartsAt                 time.Time
	FiringReceivedAt         time.Time
}

func (p *alertProof) observe(s alertSnapshot) (bool, error) {
	name, err := watchedAlertName(p.AlertName)
	if err != nil {
		return false, err
	}
	if name == "DLQMessagesDetected" && (p.SignalIdentity == "" || s.RecoveryValue == nil || s.SignalIdentity != p.SignalIdentity) {
		return false, errors.New("DLQ worker identity changed or is unknown; a counter replacement cannot prove recovery")
	}
	if !s.Receiver.StartedAt.Equal(p.ReceiverStarted) || s.Receiver.Evicted != p.Evicted {
		return false, errors.New("receiver restarted or evicted evidence during the experiment")
	}
	if s.Rule.State == "pending" && s.RecoveryValue != nil && *s.RecoveryValue > 0 {
		p.Pending = true
	}
	if s.Rule.State == "firing" && p.Pending && s.RecoveryValue != nil && *s.RecoveryValue > 0 {
		p.Firing = true
	}
	for _, n := range s.Receiver.Notifications {
		if n.Labels["alertname"] != name || n.Labels["namespace"] != mainNS || !n.ReceivedAt.After(p.Since) {
			continue
		}
		if n.Status == "firing" && p.Firing && s.ActiveFingerprints[n.Fingerprint] {
			if p.Fingerprint == "" {
				p.Fingerprint, p.StartsAt, p.FiringReceivedAt, p.Manager = n.Fingerprint, n.StartsAt, n.ReceivedAt, true
			}
		}
		if n.Status == "resolved" && p.Manager && n.Fingerprint == p.Fingerprint && n.StartsAt.Equal(p.StartsAt) &&
			n.EndsAt.After(n.StartsAt) && !n.EndsAt.After(n.ReceivedAt) && n.ReceivedAt.After(p.FiringReceivedAt) && s.Rule.State == "inactive" &&
			s.RecoveryValue != nil && *s.RecoveryValue == 0 {
			return true, nil
		}
	}
	return false, nil
}

func (e monitoringEvidence) alertWatch(ctx context.Context, name string) error {
	duration := 8 * time.Minute
	if name == "DLQMessagesDetected" {
		duration = 12 * time.Minute
	}
	bounded, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	baseline, err := e.alertSnapshot(bounded, "baseline", name)
	if err != nil {
		return err
	}
	if baseline.Rule.State != "inactive" || len(baseline.ActiveFingerprints) != 0 || baseline.RecoveryValue == nil || *baseline.RecoveryValue != 0 {
		return errors.New("restore a known healthy recovery signal (zero deficit/backlog/increase) and wait for inactive before starting a new observation")
	}
	proof := alertProof{AlertName: name, SignalIdentity: baseline.SignalIdentity, Since: time.Now().UTC(), ReceiverStarted: baseline.Receiver.StartedAt, Evicted: baseline.Receiver.Evicted}
	e.lab.print(fmt.Sprintf("Watching %s for up to %s. Follow the documented injection/recovery procedure; retain evidence on failure.", name, duration))
	if name == "DLQMessagesDetected" {
		e.lab.print("Keep the same worker running. Resolved means the 5m increase window aged out, not that the failed job or DLQ record was repaired.")
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for index := 0; ; index++ {
		select {
		case <-bounded.Done():
			return errors.New("alert cycle incomplete after bounded observation; inspect saved evidence and follow the workload recovery procedure")
		case <-ticker.C:
		}
		snapshot, err := e.alertSnapshot(bounded, fmt.Sprintf("poll-%03d", index), name)
		if err != nil {
			return err
		}
		done, err := proof.observe(snapshot)
		if err != nil {
			return err
		}
		if proof.Manager {
			e.lab.print("Firing received for " + name + " fingerprint=" + proof.Fingerprint + "; follow the recovery runbook after all required alerts are received.")
		}
		if done {
			b, err := json.MarshalIndent(proof, "", "  ")
			if err != nil {
				return err
			}
			if err := e.save("alert-cycle-proof.json", b); err != nil {
				return err
			}
			return e.lab.event("alert_cycle_completed", map[string]any{"alertname": name, "fingerprint": proof.Fingerprint, "states": "Pending/Firing/Alertmanager active/receiver firing/receiver resolved/Prometheus inactive"})
		}
	}
}
