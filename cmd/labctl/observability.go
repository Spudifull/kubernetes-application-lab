package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	observabilityNS        = mainNS + "-observability"
	monitoringRelease      = "lab-monitoring"
	monitoringChartVersion = "92.7.0"
	monitoringRepository   = "https://prometheus-community.github.io/helm-charts"
	// Conservative profile budgets, including hook/init containers and reloaders.
	// Keep these in sync with cluster/observability/monitoring-values.yaml.
	monitoringMemoryRequestMi = 1408
	monitoringMemoryLimitMi   = 2048
)

// Each invocation keeps its own evidence, including failed commands. Secret
// operations deliberately bypass this recorder; credentials never reach disk.
type monitoringEvidence struct {
	lab lab
	dir string
}

type monitoringExit struct {
	label string
	code  int
	dir   string
}

func (e *monitoringExit) Error() string {
	return fmt.Sprintf("%s exited %d; inspect %s", e.label, e.code, e.dir)
}

func (e monitoringEvidence) save(name string, data []byte) error {
	return os.WriteFile(filepath.Join(e.dir, name), data, 0600)
}

func (e monitoringEvidence) capture(ctx context.Context, label, executable string, args ...string) (string, error) {
	bounded, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	r, commandErr := e.lab.command(bounded, executable, nil, args...)
	data, err := json.MarshalIndent(struct {
		ObservedAt time.Time `json:"observed_at"`
		Command    string    `json:"command"`
		Arguments  []string  `json:"arguments"`
		ExitCode   int       `json:"exit_code"`
		Output     string    `json:"output"`
		StartError string    `json:"start_error,omitempty"`
	}{time.Now().UTC(), executable, args, r.code, r.out, errorText(commandErr)}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := e.save(label+".json", data); err != nil {
		return "", err
	}
	if commandErr != nil {
		return "", fmt.Errorf("%s: %w (evidence: %s)", label, commandErr, e.dir)
	}
	if r.code != 0 {
		return "", &monitoringExit{label: label, code: r.code, dir: e.dir}
	}
	return r.out, nil
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (e monitoringEvidence) kube(ctx context.Context, label string, args ...string) (string, error) {
	return e.capture(ctx, label, "kubectl", append([]string{"--context", e.lab.context, "--request-timeout=30s"}, args...)...)
}

func (l lab) observabilityAction(ctx context.Context, action, key string) error {
	switch action {
	case "api-alert-stop", "api-alert-start", "api-alert-check":
	case "alerts-render", "alerts-install", "alerts-status", "alerts-watch":
	case "logging-render", "logging-install", "logging-status", "logging-query", "logging-verify", "logging-retry-verify":
	case "preflight", "render", "install", "status", "targets", "query", "grafana-status", "forward-prometheus", "forward-grafana", "forward-alertmanager", "scrape-render", "scrape-fail", "scrape-restore", "application-metrics-verify", "dependencies-render", "dependencies-prepare", "dependencies-install", "dependencies-verify", "dependencies-acl-check", "tracing-render", "tracing-install", "tracing-status", "tracing-probe", "application-trace-read", "application-trace-inspect", "forward-tempo", "forward-collector":
	default:
		return errors.New("unsupported observability action")
	}
	if l.context != mainContext || l.namespace != mainNS {
		return errors.New("observability requires the main laboratory context and namespace")
	}
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	if err := l.kafkaKubectlVersion(ctx); err != nil {
		return err
	}
	dir := filepath.Join(".local-observability", time.Now().UTC().Format("20060102T150405.000000000Z")+"-"+action)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	e := monitoringEvidence{lab: l, dir: dir}
	l.print("Observability evidence: " + dir)
	switch action {
	case "api-alert-stop", "api-alert-start", "api-alert-check":
		return e.apiAvailability(ctx, action, key)
	case "alerts-render", "alerts-install", "alerts-status", "alerts-watch":
		return e.alerting(ctx, action, key)
	case "logging-render", "logging-install", "logging-status", "logging-query", "logging-verify", "logging-retry-verify":
		return e.logging(ctx, action, key)
	case "application-trace-read":
		return e.applicationTraceRead(ctx, key)
	case "application-trace-inspect":
		return e.applicationTraceInspect(ctx, key)
	case "tracing-render", "tracing-install", "tracing-status", "tracing-probe":
		return e.tracing(ctx, action)
	case "forward-tempo", "forward-collector":
		bounded, cancel := context.WithTimeout(ctx, 12*time.Minute)
		defer cancel()
		service, ports := "lab-tempo", "13200:3200"
		if action == "forward-collector" {
			service, ports = "lab-otel-collector", "14318:4318"
		}
		e.lab.print("Loopback port-forward " + ports + "; keep this terminal open (maximum 12 minutes).")
		return e.forward(bounded, service, ports)
	case "dependencies-acl-check":
		return e.dependencyACLCheck(ctx)
	case "dependencies-render", "dependencies-prepare", "dependencies-install", "dependencies-verify":
		return e.dependencyMetrics(ctx, action)
	case "preflight":
		return e.preflight(ctx)
	case "render":
		return e.render(ctx)
	case "install":
		if err := e.install(ctx); err != nil {
			return errors.Join(err, e.status(ctx))
		}
		return nil
	case "status":
		return e.status(ctx)
	case "targets":
		return e.targets(ctx)
	case "query":
		return e.query(ctx, key)
	case "application-metrics-verify":
		if err := e.targets(ctx); err != nil {
			return err
		}
		for _, key := range []string{"application-metrics-health", "publisher", "worker", "outbox", "outbox-health", "outbox-freshness"} {
			if err := e.query(ctx, key); err != nil {
				return err
			}
		}
		return nil
	case "scrape-render", "scrape-fail", "scrape-restore":
		return e.scrapeProfile(ctx, action)
	case "grafana-status":
		out, err := e.proxy(ctx, "grafana-health", "lab-monitoring-grafana:80", "/api/health")
		if err != nil {
			return err
		}
		var health struct {
			Database string `json:"database"`
		}
		if err := json.Unmarshal([]byte(out), &health); err != nil {
			return err
		}
		if health.Database != "ok" {
			return errors.New("Grafana database health is not ok")
		}
		l.print(out)
	case "forward-prometheus", "forward-grafana", "forward-alertmanager":
		services := map[string][]string{
			"forward-prometheus":   {"lab-monitoring-prometheus", "19090:9090"},
			"forward-grafana":      {"lab-monitoring-grafana", "13000:80"},
			"forward-alertmanager": {"lab-monitoring-alertmanager", "19093:9093"},
		}
		s := services[action]
		l.print("Loopback port-forward " + s[1] + "; keep this terminal open (maximum 12 minutes).")
		return e.forward(ctx, s[0], s[1])
	}
	return nil
}

func (e monitoringEvidence) preflight(ctx context.Context) error {
	checks := []struct {
		label, executable string
		args              []string
	}{
		{"git-head", "git", []string{"rev-parse", "HEAD"}},
		{"git-status", "git", []string{"status", "--short"}},
		{"helm-version", "helm", []string{"version", "--short"}},
		{"helm-releases", "helm", monitoringReleaseListArgs(e.lab.context)},
		{"docker-capacity", "docker", []string{"info", "--format", "CPUs={{.NCPU}} MemoryBytes={{.MemTotal}}"}},
		{"docker-usage", "docker", []string{"stats", "--no-stream", "--format", "{{json .}}", "kubernetes-application-lab-control-plane"}},
		{"host-memory", "docker", []string{"exec", "kubernetes-application-lab-control-plane", "cat", "/proc/meminfo"}},
	}
	for _, c := range checks {
		out, err := e.capture(ctx, c.label, c.executable, c.args...)
		if err != nil {
			return err
		}
		e.lab.print(c.label + ": " + strings.TrimSpace(out))
	}
	for _, c := range []struct {
		label string
		args  []string
	}{
		{"kubernetes-version", []string{"version", "-o", "json"}},
		{"namespaces", []string{"get", "namespaces", "-o", "json"}},
		{"nodes", []string{"get", "nodes", "-o", "json"}},
		{"node-allocations", []string{"describe", "nodes"}},
		{"storage", []string{"get", "storageclass", "-o", "json"}},
		{"crds", []string{"get", "crd", "-o", "json"}},
		{"pods", []string{"get", "pods", "-A", "-o", "json"}},
		{"application", []string{"get", "deployment,statefulset,cronjob,hpa", "-n", mainNS, "-o", "json"}},
		{"kafka", []string{"get", "kafka,kafkanodepool", "-n", kafkaNS, "-o", "json"}},
		{"keda", []string{"get", "scaledobject,triggerauthentication", "-n", mainNS, "-o", "json"}},
		{"kubelet-usage", []string{"get", "--raw", "/api/v1/nodes/kubernetes-application-lab-control-plane/proxy/stats/summary"}},
	} {
		if _, err := e.kube(ctx, c.label, c.args...); err != nil {
			return err
		}
	}
	// Missing Metrics Server is evidence of missing measurements, never zero use.
	if _, err := e.kube(ctx, "optional-top", "top", "nodes"); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, ok := errors.AsType[*monitoringExit](err); !ok {
			return err
		}
		e.lab.print("Metrics API unavailable; inspect host-memory, docker-usage and kubelet-usage instead.")
	}
	e.lab.print("Preflight saved. Review pressure, actual usage, allocations and Strimzi restarts before installing; no resources changed.")
	return nil
}

func monitoringHelmArgs() []string {
	return []string{"kube-prometheus-stack", "--repo", monitoringRepository, "--version", monitoringChartVersion,
		"--namespace", observabilityNS, "--kube-version", "1.34.0", "-f", "cluster/observability/monitoring-values.yaml"}
}

// Helm 4 removed --all from list. Explicit status filters also work in Helm 3
// and keep failed/pending releases visible to the ownership guard.
func monitoringReleaseListArgs(contextName string) []string {
	return []string{"list", "--all-namespaces", "--deployed", "--failed", "--pending", "--uninstalled", "--uninstalling", "--superseded", "--kube-context", contextName, "-o", "json"}
}

func (e monitoringEvidence) render(ctx context.Context) error {
	if err := e.dashboardValues(); err != nil {
		return err
	}
	args := append([]string{"template", monitoringRelease}, monitoringHelmArgs()...)
	args = append(args, "-f", filepath.Join(e.dir, "dashboard-values.json"))
	args = append(args, "--include-crds")
	out, err := e.capture(ctx, "helm-render", "helm", args...)
	if err != nil {
		return err
	}
	if err := e.save("monitoring-rendered.yaml", []byte(out)); err != nil {
		return err
	}
	out, err = e.capture(ctx, "application-render", "helm", "template", "lab-observability", "helm/lab-observability", "--namespace", observabilityNS, "--kube-version", "1.34.0")
	if err != nil {
		return err
	}
	if err := e.save("application-rendered.yaml", []byte(out)); err != nil {
		return err
	}
	e.lab.print("Review both rendered YAML files, images, resource limits, RBAC and selectors. No cluster resources changed.")
	return nil
}

func (e monitoringEvidence) ownership(ctx context.Context) error {
	list, err := e.capture(ctx, "helm-ownership", "helm", monitoringReleaseListArgs(e.lab.context)...)
	if err != nil {
		return err
	}
	var releases []struct{ Name, Namespace, Chart string }
	if err := json.Unmarshal([]byte(list), &releases); err != nil {
		return err
	}
	own := false
	for _, r := range releases {
		if strings.HasPrefix(r.Chart, "kube-prometheus-stack-") {
			if r.Name != monitoringRelease || r.Namespace != observabilityNS || r.Chart != "kube-prometheus-stack-"+monitoringChartVersion {
				return errors.New("another monitoring stack or chart version exists; refusing a second installation or implicit CRD upgrade")
			}
			own = true
		}
	}
	crds, err := e.kube(ctx, "monitoring-crds", "get", "crd", "-o", "json")
	if err != nil {
		return err
	}
	var objects struct {
		Items []struct{ Metadata struct{ Name string } }
	}
	if err := json.Unmarshal([]byte(crds), &objects); err != nil {
		return err
	}
	if !own {
		for _, c := range objects.Items {
			if strings.HasSuffix(c.Metadata.Name, ".monitoring.coreos.com") {
				return errors.New("monitoring CRDs already exist without this release; inspect their owner/version before installation")
			}
		}
	}
	return nil
}

func (e monitoringEvidence) install(ctx context.Context) error {
	if err := e.ownership(ctx); err != nil {
		return err
	}
	if err := e.capacity(ctx); err != nil {
		return err
	}
	if err := e.render(ctx); err != nil {
		return err
	}
	e.lab.print("Changing only the observability namespace, its Grafana Secret, Helm releases lab-monitoring/lab-observability and monitoring cluster RBAC/CRDs/webhooks. Application releases and PVCs are preserved.")
	ns := map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": observabilityNS, "labels": map[string]string{
		"pod-security.kubernetes.io/enforce": "restricted", "pod-security.kubernetes.io/enforce-version": "v1.34", "pod-security.kubernetes.io/audit": "restricted", "pod-security.kubernetes.io/warn": "restricted"}}}
	b, err := json.Marshal(ns)
	if err != nil {
		return err
	}
	for _, dry := range []bool{true, false} {
		args := []string{"apply", "-f", "-"}
		if dry {
			args = append(args, "--dry-run=server")
		}
		if _, err := e.lab.kubeInput(ctx, b, args...); err != nil {
			return err
		}
	}
	if err := e.grafanaSecret(ctx); err != nil {
		return err
	}
	// No atomic rollback/uninstall: a failure retains diagnostic objects and PVCs.
	args := []string{"upgrade", "--install", monitoringRelease, "kube-prometheus-stack", "--repo", monitoringRepository,
		"--version", monitoringChartVersion, "--namespace", observabilityNS, "--kube-context", e.lab.context,
		"--reset-values", "-f", "cluster/observability/monitoring-values.yaml", "-f", filepath.Join(e.dir, "dashboard-values.json"), "--wait", "--timeout", "300s"}
	if err := e.helmInstall(ctx, "monitoring-install", args...); err != nil {
		return err
	}
	for _, name := range []string{"servicemonitors", "podmonitors", "prometheusrules", "prometheuses", "alertmanagers"} {
		if _, err := e.kube(ctx, "crd-ready-"+name, "wait", "--for=condition=Established", "crd/"+name+".monitoring.coreos.com", "--timeout=30s"); err != nil {
			return err
		}
	}
	// Install the local monitoring resources only after the Operator's CRDs exist.
	if _, err := e.kube(ctx, "application-server-dry-run", "apply", "--dry-run=server", "-f", filepath.Join(e.dir, "application-rendered.yaml")); err != nil {
		return err
	}
	if err := e.helmInstall(ctx, "application-install", "upgrade", "--install", "lab-observability", "helm/lab-observability", "--namespace", observabilityNS,
		"--kube-context", e.lab.context, "--reset-values", "--wait", "--timeout", "60s"); err != nil {
		return err
	}
	return e.status(ctx)
}

func (e monitoringEvidence) helmInstall(ctx context.Context, label string, args ...string) error {
	// Helm wait needs a longer bound than individual inventory queries.
	r, err := e.lab.command(ctx, "helm", nil, args...)
	if saveErr := e.save(label+".txt", []byte(r.out)); saveErr != nil {
		return saveErr
	}
	if err != nil {
		return err
	}
	if r.code != 0 {
		return fmt.Errorf("%s failed; resources and evidence retained in %s; run observability-status", label, e.dir)
	}
	e.lab.print(r.out)
	return nil
}

func (e monitoringEvidence) grafanaSecret(ctx context.Context) error {
	r, err := e.lab.kubeResult(ctx, "get", "secret", "lab-grafana-admin", "-n", observabilityNS, "--ignore-not-found=true", "-o", "name")
	if err != nil || r.code != 0 {
		return errors.New("cannot inspect Grafana Secret metadata")
	}
	if strings.TrimSpace(r.out) != "" {
		return nil
	}
	password := make([]byte, 32)
	if _, err := rand.Read(password); err != nil {
		return err
	}
	secret := map[string]any{"apiVersion": "v1", "kind": "Secret", "type": "Opaque", "metadata": map[string]string{"name": "lab-grafana-admin", "namespace": observabilityNS},
		"stringData": map[string]string{"admin-user": "lab-admin", "admin-password": base64.RawURLEncoding.EncodeToString(password)}}
	b, err := json.Marshal(secret)
	if err != nil {
		return err
	}
	// create, not apply: avoid last-applied annotations containing credentials.
	r, err = e.lab.command(ctx, "kubectl", b, "--context", e.lab.context, "create", "-f", "-")
	if err != nil || r.code != 0 {
		return errors.New("cannot create Grafana credentials; inspect Secret existence without exporting its contents")
	}
	e.lab.print("Created dedicated Grafana credentials in Secret lab-grafana-admin; password not printed or saved locally.")
	return nil
}

func (e monitoringEvidence) status(ctx context.Context) error {
	var failures []error
	for _, c := range []struct {
		label string
		args  []string
	}{
		{"monitoring-workloads", []string{"get", "deployment,statefulset,pod,service,pvc", "-n", observabilityNS, "-o", "json"}},
		{"monitoring-resources", []string{"get", "prometheus,alertmanager,servicemonitor,podmonitor,prometheusrule", "-n", observabilityNS, "-o", "json"}},
		{"monitoring-events", []string{"get", "events", "-n", observabilityNS, "--sort-by=.metadata.creationTimestamp"}},
		{"application-state", []string{"get", "deployment,statefulset,cronjob,hpa", "-n", mainNS, "-o", "wide"}},
	} {
		out, err := e.kube(ctx, c.label, c.args...)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if c.label == "application-state" || c.label == "monitoring-events" {
			e.lab.print(out)
		}
	}
	for _, release := range []string{monitoringRelease, "lab-observability"} {
		out, err := e.capture(ctx, "release-"+release, "helm", "status", release, "--namespace", observabilityNS, "--kube-context", e.lab.context, "-o", "json")
		if err != nil {
			failures = append(failures, err)
			continue
		}
		// Helm status JSON includes entire release manifests. Keep them in the
		// evidence file, but avoid flooding the terminal with CRD schemas.
		var summary struct {
			Name, Namespace string
			Version         int
			Info            struct{ Status string }
		}
		if err := json.Unmarshal([]byte(out), &summary); err != nil {
			failures = append(failures, err)
			continue
		}
		if err := e.lab.event("monitoring_release", map[string]any{"name": summary.Name, "namespace": summary.Namespace, "revision": summary.Version, "status": summary.Info.Status}); err != nil {
			failures = append(failures, err)
		}
	}
	for _, service := range []string{"lab-monitoring-prometheus:9090", "lab-monitoring-alertmanager:9093"} {
		out, err := e.proxy(ctx, strings.Split(service, ":")[0]+"-ready", service, "/-/ready")
		if err != nil {
			failures = append(failures, err)
			continue
		}
		e.lab.print(strings.TrimSpace(out))
	}
	return errors.Join(failures...)
}

func (e monitoringEvidence) proxy(ctx context.Context, label, service, path string) (string, error) {
	return e.kube(ctx, label, "get", "--raw", "/api/v1/namespaces/"+observabilityNS+"/services/http:"+service+"/proxy"+path)
}

func (e monitoringEvidence) targets(ctx context.Context) error {
	out, err := e.proxy(ctx, "prometheus-targets", "lab-monitoring-prometheus:9090", "/api/v1/targets?state=active")
	if err != nil {
		return err
	}
	var targets struct {
		Status string
		Data   struct {
			ActiveTargets []struct {
				Labels                       map[string]string
				Health, LastError, ScrapeURL string
			}
		}
	}
	if err := json.Unmarshal([]byte(out), &targets); err != nil {
		return err
	}
	if targets.Status != "success" || len(targets.Data.ActiveTargets) == 0 {
		return errors.New("Prometheus returned no active targets")
	}
	apiFound, healthy := false, true
	for _, t := range targets.Data.ActiveTargets {
		if t.Labels["namespace"] == mainNS && t.Labels["service"] == "lab-api" {
			apiFound = true
		}
		if t.Health != "up" {
			healthy = false
		}
		if err := e.lab.event("prometheus_target", map[string]any{"job": t.Labels["job"], "namespace": t.Labels["namespace"], "service": t.Labels["service"], "health": t.Health, "last_error": t.LastError, "scrape_url": t.ScrapeURL}); err != nil {
			return err
		}
	}
	if !apiFound || !healthy {
		return errors.New("API target missing or at least one selected target is not UP; inspect saved targets")
	}
	return nil
}

func monitoringQuery(key string) (string, error) {
	queries := map[string]string{
		"pg-up":               `pg_up{namespace="` + observabilityNS + `",job="lab-postgres-exporter"}`,
		"pg-connections":      `sum(pg_stat_database_numbackends{namespace="` + observabilityNS + `",job="lab-postgres-exporter"})`,
		"pg-max-connections":  `pg_settings_max_connections{namespace="` + observabilityNS + `",job="lab-postgres-exporter"}`,
		"pg-connection-ratio": `sum(pg_stat_database_numbackends{namespace="` + observabilityNS + `",job="lab-postgres-exporter"}) / max(pg_settings_max_connections{namespace="` + observabilityNS + `",job="lab-postgres-exporter"})`,
		"pg-transactions":     `pg_stat_database_xact_commit{namespace="` + observabilityNS + `",job="lab-postgres-exporter",datname="lab"}`,
		"pg-states":           `pg_stat_activity_count{namespace="` + observabilityNS + `",job="lab-postgres-exporter",datname="lab"}`,
		"pg-rollbacks":        `pg_stat_database_xact_rollback{namespace="` + observabilityNS + `",job="lab-postgres-exporter",datname="lab"}`,
		"pg-collector-errors": `pg_exporter_last_scrape_error{namespace="` + observabilityNS + `",job="lab-postgres-exporter"}`,
		"pg-collectors":       `pg_scrape_collector_success{namespace="` + observabilityNS + `",job="lab-postgres-exporter"}`,
		"kafka-brokers":       `kafka_brokers{namespace="` + observabilityNS + `",job="lab-kafka-exporter"}`,
		"kafka-lag":           `kafka_consumergroup_lag{namespace="` + observabilityNS + `",job="lab-kafka-exporter",consumergroup="lab-workers",topic="lab.jobs.v1"}`,
		"kafka-members":       `kafka_consumergroup_members{namespace="` + observabilityNS + `",job="lab-kafka-exporter",consumergroup="lab-workers"}`,
		"kafka-requests":      `lab_kafka_broker_requests_total{namespace="` + kafkaNS + `",job="lab-kafka-jmx"}`,
		"kafka-errors":        `lab_kafka_broker_errors_total{namespace="` + kafkaNS + `",job="lab-kafka-jmx",error!="NONE"}`,
		"dependencies-health": `(min(up{namespace="` + observabilityNS + `",job="lab-postgres-exporter"})==1) and on() (min(pg_up{namespace="` + observabilityNS + `",job="lab-postgres-exporter"})==1) and on() (min(pg_exporter_last_scrape_error{namespace="` + observabilityNS + `",job="lab-postgres-exporter"})==0) and on() (min(up{namespace="` + observabilityNS + `",job="lab-kafka-exporter"})==1) and on() (min(kafka_brokers{namespace="` + observabilityNS + `",job="lab-kafka-exporter"})==1) and on() (min(up{namespace="` + kafkaNS + `",job="lab-kafka-jmx"})==1) and on() (count(kafka_consumergroup_lag{namespace="` + observabilityNS + `",job="lab-kafka-exporter",consumergroup="lab-workers",topic="lab.jobs.v1"})==3) and on() (min(kafka_consumergroup_lag{namespace="` + observabilityNS + `",job="lab-kafka-exporter",consumergroup="lab-workers",topic="lab.jobs.v1"})>=0)`,
		"up":                  "up", "build": `lab_build_info{namespace="` + mainNS + `",service="lab-api"}`,
		"api-up":                     `up{namespace="` + mainNS + `",service="lab-api"}`,
		"build-all":                  `lab_build_info{namespace="` + mainNS + `"}`,
		"api-requests":               `sum(rate(lab_http_requests_total{namespace="` + mainNS + `",service="lab-api"}[2m]))`,
		"api-recording-rule":         "lab:api_targets_up:sum",
		"worker-ready":               `kube_deployment_status_replicas_available{namespace="` + mainNS + `",deployment="lab-worker"}`,
		"publisher":                  `lab_publisher_outbox_published_total{namespace="` + mainNS + `"}`,
		"publisher-errors":           `lab_publisher_publish_errors_total{namespace="` + mainNS + `"}`,
		"worker":                     `lab_worker_jobs_processed_total{namespace="` + mainNS + `"}`,
		"worker-errors":              `lab_worker_jobs_failed_total{namespace="` + mainNS + `"}`,
		"worker-duplicates":          `lab_worker_jobs_duplicates_total{namespace="` + mainNS + `"}`,
		"worker-retries":             `lab_worker_jobs_retried_total{namespace="` + mainNS + `"}`,
		"worker-dlq":                 `lab_worker_jobs_dlq_total{namespace="` + mainNS + `"}`,
		"outbox":                     `lab_outbox_pending_events{namespace="` + mainNS + `"}`,
		"outbox-age":                 `lab_outbox_oldest_pending_age_seconds{namespace="` + mainNS + `"}`,
		"outbox-health":              `lab_outbox_stats_up{namespace="` + mainNS + `"}`,
		"outbox-freshness":           `time()-lab_outbox_stats_last_success_timestamp_seconds{namespace="` + mainNS + `"}`,
		"api-p95":                    `histogram_quantile(0.95,sum by(le)(rate(lab_http_request_latency_seconds_bucket{namespace="` + mainNS + `",route!~"GET /(metrics|livez|readyz|version)"}[5m])))`,
		"application-metrics-health": `(min(up{namespace="` + mainNS + `",job="lab-publisher"}) == 1) and on() (min(up{namespace="` + mainNS + `",job="lab-worker"}) == 1) and on() (min(lab_outbox_stats_up{namespace="` + mainNS + `"}) == 1) and on() (max(time()-lab_outbox_stats_last_success_timestamp_seconds{namespace="` + mainNS + `"}) < 30)`,
	}
	q, ok := queries[key]
	if !ok {
		return "", errors.New("unknown query key; use a documented key from observability-lab.md")
	}
	if key == "dependencies-health" {
		// The legacy last_scrape_error gauge does not cover the newer collector set.
		q += ` and on() (min(pg_scrape_collector_success{namespace="` + observabilityNS + `",job="lab-postgres-exporter"})==1)`
	}
	return q, nil
}

func (e monitoringEvidence) query(ctx context.Context, key string) error {
	if key == "" {
		key = "up"
	}
	q, err := monitoringQuery(key)
	if err != nil {
		return err
	}
	out, err := e.proxy(ctx, "query-"+key, "lab-monitoring-prometheus:9090", "/api/v1/query?query="+url.QueryEscape(q))
	if err != nil {
		return err
	}
	var response struct {
		Status string
		Data   struct{ Result []json.RawMessage }
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		return err
	}
	e.lab.print(out)
	if response.Status != "success" || len(response.Data.Result) == 0 {
		return errors.New("PromQL returned an error or no samples; installation is not scrape evidence")
	}
	return nil
}

// The first profile is deliberately limited to one kind node. Requests are
// scheduling reservations; the sum of specified limits does not measure usage.
func (e monitoringEvidence) capacity(ctx context.Context) error {
	baseline, err := e.kube(ctx, "capacity-application", "get", "deployment/lab-api", "deployment/lab-publisher", "deployment/lab-worker", "statefulset/lab-postgres", "-n", mainNS, "-o", "json")
	if err != nil {
		return err
	}
	if err := monitoringBaseline([]byte(baseline)); err != nil {
		return err
	}
	var kafka struct {
		Status struct {
			Conditions []struct{ Type, Status string }
		}
	}
	state, err := e.kube(ctx, "capacity-kafka", "get", "kafka/lab-kafka", "-n", kafkaNS, "-o", "json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(state), &kafka); err != nil {
		return err
	}
	kafkaReady := false
	for _, c := range kafka.Status.Conditions {
		if c.Type == "Ready" && c.Status == "True" {
			kafkaReady = true
		}
	}
	if !kafkaReady {
		return errors.New("restore Kafka readiness before installing monitoring")
	}
	nodes, err := e.kube(ctx, "capacity-nodes", "get", "nodes", "-o", "json")
	if err != nil {
		return err
	}
	pods, err := e.kube(ctx, "capacity-pods", "get", "pods", "-A", "-o", "json")
	if err != nil {
		return err
	}
	if err := monitoringCapacity([]byte(nodes), []byte(pods)); err != nil {
		return err
	}
	// Docker 25 / this WSL kernel can under-report cgroup working sets. Use
	// Linux MemAvailable as a separate host-wide safety signal, not a Pod metric.
	meminfo, err := e.capture(ctx, "capacity-host-memory", "docker", "exec", "kubernetes-application-lab-control-plane", "cat", "/proc/meminfo")
	if err != nil {
		return err
	}
	return monitoringAvailableMemory(meminfo)
}

func monitoringBaseline(data []byte) error {
	var list struct {
		Items []struct {
			Metadata struct {
				Name       string
				Generation int64
			}
			Spec   struct{ Replicas int }
			Status struct {
				ReadyReplicas      int
				ObservedGeneration int64
			}
		}
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	wanted := map[string]bool{"lab-api": false, "lab-publisher": false, "lab-worker": false, "lab-postgres": false}
	for _, obj := range list.Items {
		if _, ok := wanted[obj.Metadata.Name]; !ok {
			continue
		}
		if obj.Spec.Replicas < 1 || obj.Status.ReadyReplicas != obj.Spec.Replicas || obj.Status.ObservedGeneration < obj.Metadata.Generation {
			return fmt.Errorf("restore %s readiness before installing monitoring", obj.Metadata.Name)
		}
		wanted[obj.Metadata.Name] = true
	}
	for name, found := range wanted {
		if !found {
			return fmt.Errorf("baseline workload %s missing", name)
		}
	}
	return nil
}

func monitoringAvailableMemory(meminfo string) error {
	for _, line := range strings.Split(meminfo, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "MemAvailable:" && fields[2] == "kB" {
			n, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil || n < 0 || n > (1<<63-1)/1024 {
				return errors.New("invalid MemAvailable measurement")
			}
			if n*1024 < (monitoringMemoryLimitMi+512)*(1<<20) {
				return errors.New("insufficient actual host memory for monitoring plus reserve; preserve existing workload budgets")
			}
			return nil
		}
	}
	return errors.New("MemAvailable missing; actual memory headroom is unverified")
}

func (e monitoringEvidence) forward(ctx context.Context, service, ports string) error {
	r, err := e.lab.command(ctx, "kubectl", nil, "--context", e.lab.context, "--namespace", observabilityNS, "port-forward", "--address", "127.0.0.1", "--pod-running-timeout=30s", "service/"+service, ports)
	if saveErr := e.save("port-forward.txt", []byte(r.out)); saveErr != nil {
		return saveErr
	}
	if err != nil {
		return err
	}
	if r.code != 0 {
		return fmt.Errorf("port-forward exited %d; inspect %s", r.code, e.dir)
	}
	return nil
}

type monitoringResources struct {
	Requests map[string]string `json:"requests"`
	Limits   map[string]string `json:"limits"`
}

type monitoringPodMetadata struct {
	Namespace       string
	Labels          map[string]string
	OwnerReferences []struct {
		Kind, Name string
		Controller bool
	}
}

func monitoringOwnedPod(meta monitoringPodMetadata) bool {
	if meta.Namespace != observabilityNS {
		return false
	}
	if meta.Labels["app.kubernetes.io/instance"] == monitoringRelease {
		return true
	}
	if meta.Labels["app.kubernetes.io/managed-by"] != "prometheus-operator" {
		return false
	}
	// The Operator uses the CR name as instance, not the Helm release name.
	// Match the exact instance and its controlling StatefulSet, not a prefix.
	owners := map[string]string{
		"lab-monitoring-prometheus":   "prometheus-lab-monitoring-prometheus",
		"lab-monitoring-alertmanager": "alertmanager-lab-monitoring-alertmanager",
	}
	owner, ok := owners[meta.Labels["app.kubernetes.io/instance"]]
	if !ok {
		return false
	}
	for _, ref := range meta.OwnerReferences {
		if ref.Controller && ref.Kind == "StatefulSet" && ref.Name == owner {
			return true
		}
	}
	return false
}

func memoryBytes(q string) (int64, error) {
	if q == "" {
		return 0, nil
	}
	multipliers := []struct {
		suffix string
		value  int64
	}{{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40}, {"K", 1000}, {"M", 1000000}, {"G", 1000000000}, {"", 1}}
	for _, u := range multipliers {
		if !strings.HasSuffix(q, u.suffix) {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSuffix(q, u.suffix), 10, 64)
		if err != nil || n < 0 || n > (1<<63-1)/u.value {
			return 0, fmt.Errorf("unsupported memory quantity %q", q)
		}
		return n * u.value, nil
	}
	return 0, fmt.Errorf("unsupported memory quantity %q", q)
}

func monitoringCapacity(nodesJSON, podsJSON []byte) error {
	var nodes struct {
		Items []struct {
			Status struct {
				Allocatable map[string]string
				Conditions  []struct{ Type, Status string }
			}
		}
	}
	if err := json.Unmarshal(nodesJSON, &nodes); err != nil {
		return err
	}
	if len(nodes.Items) != 1 {
		return errors.New("monitoring profile requires exactly one kind node; review sizing for this topology")
	}
	n := nodes.Items[0]
	ready := false
	for _, c := range n.Status.Conditions {
		if c.Type == "Ready" && c.Status == "True" {
			ready = true
		}
		if (c.Type == "MemoryPressure" || c.Type == "DiskPressure" || c.Type == "PIDPressure") && c.Status != "False" {
			return errors.New("node pressure prevents monitoring installation")
		}
	}
	if !ready {
		return errors.New("node is not Ready")
	}
	memory, err := memoryBytes(n.Status.Allocatable["memory"])
	if err != nil {
		return err
	}
	cpu, err := parseCPU(n.Status.Allocatable["cpu"])
	if err != nil {
		return err
	}
	var pods struct {
		Items []struct {
			Metadata monitoringPodMetadata
			Status   struct{ Phase string }
			Spec     struct {
				Containers     []struct{ Resources monitoringResources }
				InitContainers []struct {
					Resources     monitoringResources
					RestartPolicy string
				}
				Overhead map[string]string
			}
		}
	}
	if err := json.Unmarshal(podsJSON, &pods); err != nil {
		return err
	}
	var requests, limits, cpuRequests int64
	for _, p := range pods.Items {
		if p.Status.Phase == "Succeeded" || p.Status.Phase == "Failed" {
			continue
		}
		// The budget replaces this release, not foreign Pods in the namespace.
		// Its 256 Mi reserve includes the operator/exporter rolling surge;
		// Grafana uses Recreate and Prometheus uses a single StatefulSet replica.
		if monitoringOwnedPod(p.Metadata) {
			continue
		}
		var pr, pl, pc int64
		add := func(r monitoringResources, init bool) error {
			mr, err := memoryBytes(r.Requests["memory"])
			if err != nil {
				return err
			}
			ml, err := memoryBytes(r.Limits["memory"])
			if err != nil {
				return err
			}
			var cr int64
			if r.Requests["cpu"] != "" {
				cr, err = parseCPU(r.Requests["cpu"])
				if err != nil {
					return err
				}
			}
			if init {
				pr = max(pr, mr)
				pl = max(pl, ml)
				pc = max(pc, cr)
			} else {
				pr += mr
				pl += ml
				pc += cr
			}
			return nil
		}
		for _, c := range p.Spec.Containers {
			if err := add(c.Resources, false); err != nil {
				return err
			}
		}
		for _, c := range p.Spec.InitContainers {
			if c.RestartPolicy == "Always" {
				return errors.New("restartable init container requires explicit capacity review")
			}
			if err := add(c.Resources, true); err != nil {
				return err
			}
		}
		overhead, err := memoryBytes(p.Spec.Overhead["memory"])
		if err != nil {
			return err
		}
		requests += pr + overhead
		limits += pl + overhead
		cpuRequests += pc
	}
	// Conservative ceiling for this versioned values file, including hook/init
	// containers and reloaders. Revisit together with every profile change.
	if requests+(monitoringMemoryRequestMi+512)*(1<<20) > memory || limits+(monitoringMemoryLimitMi+256)*(1<<20) > memory || cpuRequests+500+500 > cpu {
		return fmt.Errorf("insufficient conservative monitoring headroom: memory requests=%dMi limits=%dMi allocatable=%dMi CPU requests=%dm allocatable=%dm; keep existing workload budgets and increase capacity or review sizing", requests>>20, limits>>20, memory>>20, cpuRequests, cpu)
	}
	return nil
}
