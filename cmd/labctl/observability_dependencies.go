package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const dependencyRelease = "lab-dependency-metrics"

// Secret commands deliberately bypass capture and kubeInput: stderr or SQL
// diagnostics could contain credentials. Only generic failures escape.
func (e monitoringEvidence) dependencySecret(ctx context.Context, ns, name string) (map[string][]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	r, err := e.lab.kubeResult(ctx, "get", "secret", name, "-n", ns, "--ignore-not-found=true", "-o", "json")
	if err != nil || r.code != 0 {
		return nil, errors.New("cannot read dependency credential; output suppressed")
	}
	if strings.TrimSpace(r.out) == "" {
		return nil, nil
	}
	var s struct {
		Metadata struct{ Labels map[string]string }
		Data     map[string][]byte
	}
	if json.Unmarshal([]byte(r.out), &s) != nil {
		return nil, errors.New("invalid dependency credential JSON")
	}
	if ns == observabilityNS && s.Metadata.Labels["lab-metrics-owner"] != dependencyRelease {
		return nil, errors.New("refusing unrelated dependency Secret")
	}
	return s.Data, nil
}

func (e monitoringEvidence) createDependencySecret(ctx context.Context, name string, data map[string][]byte) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	b, err := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "Secret", "type": "Opaque",
		"metadata": map[string]any{"name": name, "namespace": observabilityNS, "labels": map[string]string{"lab-metrics-owner": dependencyRelease}}, "data": data})
	if err != nil {
		return err
	}
	r, err := e.lab.command(ctx, "kubectl", b, "--context", e.lab.context, "create", "-f", "-")
	if err != nil || r.code != 0 {
		return errors.New("cannot create dependency credential; output suppressed")
	}
	return nil
}

func dependencyRoleSQL(password []byte) ([]byte, error) {
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).Match(password) {
		return nil, errors.New("invalid monitoring password encoding")
	}
	script, err := os.ReadFile("cluster/observability/postgres-monitor-role.sql")
	if err != nil {
		return nil, err
	}
	return append([]byte("\\set metrics_password '"+string(password)+"'\n"), script...), nil
}

func (e monitoringEvidence) prepareDependencies(ctx context.Context) error {
	if err := e.capacity(ctx); err != nil {
		return err
	}
	if err := e.dependencyKafkaBaseline(ctx); err != nil {
		return err
	}
	// The monitoring namespace must already exist; preparation never installs a second stack.
	if _, err := e.kube(ctx, "dependency-namespace", "get", "namespace", observabilityNS); err != nil {
		return err
	}
	pg, err := e.dependencySecret(ctx, observabilityNS, "lab-metrics-postgres")
	if err != nil {
		return err
	}
	if pg == nil {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return err
		}
		pg = map[string][]byte{"password": []byte(hex.EncodeToString(secret))}
		if err := e.createDependencySecret(ctx, "lab-metrics-postgres", pg); err != nil {
			return err
		}
	}
	sql, err := dependencyRoleSQL(pg["password"])
	if err != nil {
		return err
	}
	sqlCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	r, err := e.lab.command(sqlCtx, "kubectl", sql, "--context", e.lab.context, "exec", "-i", "pod/lab-postgres-0", "-n", mainNS,
		"--", "psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", "lab", "-d", "lab")
	cancel()
	if err != nil || r.code != 0 {
		return errors.New("monitoring role preparation failed; output suppressed; inspect role grants without exporting credentials")
	}
	for _, dry := range []bool{true, false} {
		args := []string{"apply", "-f", "cluster/kafka/monitoring-user.yaml"}
		if dry {
			args = append(args, "--dry-run=server")
		}
		if _, err := e.kube(ctx, "monitoring-user-"+map[bool]string{true: "dry-run", false: "apply"}[dry], args...); err != nil {
			return err
		}
	}
	if _, err := e.kube(ctx, "monitoring-user-ready", "wait", "--for=condition=Ready", "kafkauser/lab-metrics", "-n", kafkaNS, "--timeout=30s"); err != nil {
		return err
	}
	ca, err := e.dependencySecret(ctx, kafkaNS, "lab-kafka-cluster-ca-cert")
	if err != nil {
		return err
	}
	user, err := e.dependencySecret(ctx, kafkaNS, "lab-metrics")
	if err != nil {
		return err
	}
	wanted := map[string][]byte{"ca.crt": ca["ca.crt"], "user.crt": user["user.crt"], "user.key": user["user.key"]}
	for _, v := range wanted {
		if len(v) == 0 {
			return errors.New("monitoring Kafka identity incomplete")
		}
	}
	old, err := e.dependencySecret(ctx, observabilityNS, "lab-metrics-kafka")
	if err != nil {
		return err
	}
	if old == nil {
		if err := e.createDependencySecret(ctx, "lab-metrics-kafka", wanted); err != nil {
			return err
		}
	} else {
		for k, v := range wanted {
			if !bytes.Equal(old[k], v) {
				return errors.New("Kafka monitoring certificate changed; review rotation before replacing credential")
			}
		}
	}
	e.lab.print("Dedicated SQL role and Kafka metadata identity prepared; no credentials saved in evidence.")
	return nil
}

func (e monitoringEvidence) dependencyReleaseGuard(ctx context.Context) error {
	out, err := e.capture(ctx, "dependency-release-ownership", "helm", monitoringReleaseListArgs(e.lab.context)...)
	if err != nil {
		return err
	}
	return dependencyReleaseOwnership([]byte(out))
}

func dependencyReleaseOwnership(data []byte) error {
	var releases []struct{ Name, Namespace, Chart, Status string }
	if err := json.Unmarshal(data, &releases); err != nil {
		return err
	}
	for _, r := range releases {
		if r.Name == dependencyRelease || strings.HasPrefix(r.Chart, "lab-dependency-metrics-") {
			if r.Name != dependencyRelease || r.Namespace != observabilityNS || r.Chart != "lab-dependency-metrics-0.1.0" || (r.Status != "deployed" && r.Status != "failed") {
				return errors.New("dependency release owner/version/state is not supported")
			}
		}
	}
	return nil
}

func (e monitoringEvidence) dependencyMetrics(ctx context.Context, action string) error {
	if action == "dependencies-prepare" {
		return e.prepareDependencies(ctx)
	}
	if action == "dependencies-verify" {
		if err := e.targets(ctx); err != nil {
			return err
		}
		for _, key := range []string{"dependencies-health", "pg-collectors", "pg-connections", "pg-max-connections", "pg-transactions", "pg-states", "kafka-lag", "kafka-members", "kafka-requests"} {
			if err := e.query(ctx, key); err != nil {
				return err
			}
		}
		return nil
	}
	out, err := e.capture(ctx, "dependency-render", "helm", "template", dependencyRelease, "helm/lab-dependency-metrics", "--namespace", observabilityNS, "--kube-version", "1.34.0")
	if err != nil {
		return err
	}
	if err := e.save("dependencies-rendered.yaml", []byte(out)); err != nil {
		return err
	}
	if action == "dependencies-render" {
		return nil
	}
	if err := e.dependencyReleaseGuard(ctx); err != nil {
		return err
	}
	if err := e.dependencyKafkaBaseline(ctx); err != nil {
		return err
	}
	if err := e.capacity(ctx); err != nil {
		return err
	}
	// Charge an extra conservative reservation without shrinking existing workloads.
	nodes, err := e.kube(ctx, "dependency-capacity-nodes", "get", "nodes", "-o", "json")
	if err != nil {
		return err
	}
	pods, err := e.kube(ctx, "dependency-capacity-pods", "get", "pods", "-A", "-o", "json")
	if err != nil {
		return err
	}
	if err := dependencyCapacity([]byte(nodes), []byte(pods)); err != nil {
		return err
	}
	for _, name := range []string{"lab-metrics-postgres", "lab-metrics-kafka"} {
		data, err := e.dependencySecret(ctx, observabilityNS, name)
		if err != nil {
			return err
		}
		if data == nil {
			return errors.New("run dependency-metrics-prepare before installation")
		}
	}
	if _, err := e.kube(ctx, "dependency-dry-run", "apply", "--dry-run=server", "-f", filepath.Join(e.dir, "dependencies-rendered.yaml")); err != nil {
		return err
	}
	// Snapshot Kafka and storage before the explicitly requested broker roll.
	for i, args := range [][]string{
		{"get", "kafka/lab-kafka", "-n", kafkaNS, "-o", "json"},
		{"get", "pvc", "-n", kafkaNS, "-o", "json"},
	} {
		if _, err := e.kube(ctx, []string{"dependency-kafka-before", "dependency-pvc-before"}[i], args...); err != nil {
			return err
		}
	}
	e.lab.print("Installing exporters and enabling JMX may roll the single broker/controller; PVC, topics and offsets are not changed. Keep evidence on failure.")
	if err := e.helmInstall(ctx, "dependency-install", "upgrade", "--install", dependencyRelease, "helm/lab-dependency-metrics", "--namespace", observabilityNS,
		"--kube-context", e.lab.context, "--reset-values", "--wait", "--timeout", "180s"); err != nil {
		return err
	}
	if _, err := e.kube(ctx, "dependency-jmx-dry-run", "patch", "kafka/lab-kafka", "-n", kafkaNS, "--type=merge", "--patch-file", "cluster/kafka/metrics-patch.json", "--dry-run=server"); err != nil {
		return err
	}
	if _, err := e.kube(ctx, "dependency-jmx-enable", "patch", "kafka/lab-kafka", "-n", kafkaNS, "--type=merge", "--patch-file", "cluster/kafka/metrics-patch.json"); err != nil {
		return err
	}
	e.lab.print("Wait for Kafka observedGeneration to match metadata.generation and the broker metrics port; Helm deployed or an old Ready condition alone is insufficient. Then run dependency-metrics-verify and worker-group-status.")
	return nil
}

func (e monitoringEvidence) dependencyKafkaBaseline(ctx context.Context) error {
	out, err := e.kube(ctx, "dependency-kafka-baseline", "get", "kafka/lab-kafka", "-n", kafkaNS, "-o", "json")
	if err != nil {
		return err
	}
	if err := dependencyKafkaConfig([]byte(out)); err != nil {
		return err
	}
	out, err = e.kube(ctx, "dependency-operator-baseline", "get", "deployment/strimzi-cluster-operator", "-n", kafkaNS, "-o", "json")
	if err != nil {
		return err
	}
	var d struct {
		Metadata struct{ Generation int64 }
		Spec     struct{ Replicas int }
		Status   struct {
			ObservedGeneration int64
			ReadyReplicas      int
		}
	}
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		return err
	}
	if d.Spec.Replicas != 1 || d.Status.ReadyReplicas != 1 || d.Status.ObservedGeneration != d.Metadata.Generation {
		return errors.New("restore one Ready Strimzi operator before dependency monitoring changes")
	}
	return nil
}

func dependencyKafkaConfig(data []byte) error {
	var k struct {
		Metadata struct {
			Generation  int64
			Annotations map[string]string
		}
		Spec struct {
			Kafka struct {
				Version       string
				MetricsConfig *struct {
					Type      string
					ValueFrom struct{ ConfigMapKeyRef struct{ Name, Key string } }
				}
			}
		}
		Status struct {
			ObservedGeneration int64
			Conditions         []struct{ Type, Status string }
		}
	}
	if err := json.Unmarshal(data, &k); err != nil {
		return err
	}
	if k.Spec.Kafka.Version != "4.2.2" || k.Metadata.Annotations["strimzi.io/pause-reconciliation"] == "true" || k.Status.ObservedGeneration != k.Metadata.Generation {
		return errors.New("Kafka version or reconciliation baseline requires review")
	}
	ready := false
	for _, c := range k.Status.Conditions {
		if c.Type == "Ready" && c.Status == "True" {
			ready = true
		}
	}
	if !ready {
		return errors.New("restore Kafka readiness before enabling metrics")
	}
	if m := k.Spec.Kafka.MetricsConfig; m != nil && (m.Type != "jmxPrometheusExporter" || m.ValueFrom.ConfigMapKeyRef.Name != "lab-kafka-jmx-metrics" || m.ValueFrom.ConfigMapKeyRef.Key != "metrics.yaml") {
		return errors.New("refusing to overwrite another Kafka metrics configuration")
	}
	return nil
}

func dependencyCapacity(nodes, pods []byte) error {
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(pods, &list); err != nil {
		return err
	}
	// 128Mi exporter requests + 128Mi allowance for the in-process JMX agent;
	// 256Mi exporter limits + 128Mi agent allowance. Existing JVM limit stays 2Gi.
	list.Items = append(list.Items, json.RawMessage(`{"spec":{"containers":[{"resources":{"requests":{"memory":"256Mi","cpu":"100m"},"limits":{"memory":"384Mi"}}}]}}`))
	data, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return monitoringCapacity(nodes, data)
}
