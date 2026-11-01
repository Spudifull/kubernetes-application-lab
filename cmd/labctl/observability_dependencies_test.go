package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDependencyMetricsContextGuard(t *testing.T) {
	for _, action := range []string{"dependencies-render", "dependencies-prepare", "dependencies-install", "dependencies-verify", "dependencies-acl-check"} {
		f := &fakeRunner{}
		l := lab{context: netContext, namespace: mainNS, runner: f}
		if l.observabilityAction(context.Background(), action, "") == nil || len(f.calls) != 0 {
			t.Fatalf("unguarded dependency action %s", action)
		}
	}
}

func TestDependencyReleaseOwnership(t *testing.T) {
	for _, tc := range []struct {
		data string
		ok   bool
	}{
		{`[]`, true},
		{`[{"name":"lab-dependency-metrics","namespace":"` + observabilityNS + `","chart":"lab-dependency-metrics-0.1.0","status":"deployed"}]`, true},
		{`[{"name":"lab-dependency-metrics","namespace":"other","chart":"lab-dependency-metrics-0.1.0","status":"deployed"}]`, false},
		{`[{"name":"other","namespace":"` + observabilityNS + `","chart":"lab-dependency-metrics-0.1.0","status":"deployed"}]`, false},
		{`[{"name":"lab-dependency-metrics","namespace":"` + observabilityNS + `","chart":"lab-dependency-metrics-0.2.0","status":"deployed"}]`, false},
		{`[{"name":"lab-dependency-metrics","namespace":"` + observabilityNS + `","chart":"lab-dependency-metrics-0.1.0","status":"pending-upgrade"}]`, false},
	} {
		if (dependencyReleaseOwnership([]byte(tc.data)) == nil) != tc.ok {
			t.Fatal(tc.data)
		}
	}
}

func TestDependencyKafkaGuard(t *testing.T) {
	base := `{"metadata":{"generation":4},"spec":{"kafka":{"version":"4.2.2"}},"status":{"observedGeneration":4,"conditions":[{"type":"Ready","status":"True"}]}}`
	for _, tc := range []struct {
		data string
		ok   bool
	}{
		{base, true},
		{strings.Replace(base, `"observedGeneration":4`, `"observedGeneration":3`, 1), false},
		{strings.Replace(base, `"status":"True"`, `"status":"False"`, 1), false},
		{strings.Replace(base, `"4.2.2"`, `"4.3.0"`, 1), false},
		{strings.Replace(base, `"version":"4.2.2"`, `"version":"4.2.2","metricsConfig":{"type":"other"}`, 1), false},
	} {
		if (dependencyKafkaConfig([]byte(tc.data)) == nil) != tc.ok {
			t.Fatal(tc.data)
		}
	}
}

func TestDependencyCapacityChargesNewExporters(t *testing.T) {
	nodes := []byte(`{"items":[{"status":{"allocatable":{"memory":"2556Mi","cpu":"12"},"conditions":[{"type":"Ready","status":"True"}]}}]}`)
	pods := []byte(`{"items":[]}`)
	if err := monitoringCapacity(nodes, pods); err != nil {
		t.Fatal(err)
	}
	if dependencyCapacity(nodes, pods) == nil {
		t.Fatal("new exporter/JMX reservation ignored")
	}
}

func TestDependencySQLPasswordEncoding(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	for _, bad := range []string{"", "pass'\\quit", strings.Repeat("g", 64), strings.Repeat("a", 63)} {
		if _, err := dependencyRoleSQL([]byte(bad)); err == nil {
			t.Fatal("accepted unsafe monitoring password")
		}
	}
	password := strings.Repeat("a", 64)
	b, err := dependencyRoleSQL([]byte(password))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\\set metrics_password '" + password + "'", "NOSUPERUSER NOCREATEDB NOCREATEROLE", "GRANT pg_monitor", "CONNECTION LIMIT 2", "default_transaction_read_only", "has_table_privilege", "has_schema_privilege"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing SQL guard %q", want)
		}
	}
}

type dependencySecretRunner struct {
	password string
	args     []string
	input    []byte
}

func (f *dependencySecretRunner) run(_ context.Context, _ string, args []string, input []byte) (result, error) {
	f.args = append([]string{}, args...)
	f.input = bytes.Clone(input)
	return result{code: 1, out: f.password}, errors.New(f.password)
}

func TestDependencySecretFailureNeverLeaks(t *testing.T) {
	secret := "sentinel-credential"
	f := &dependencySecretRunner{password: secret}
	var output bytes.Buffer
	e := monitoringEvidence{lab: lab{context: mainContext, runner: f, out: &output}, dir: t.TempDir()}
	err := e.createDependencySecret(context.Background(), "lab-metrics-postgres", map[string][]byte{"password": []byte(secret)})
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(output.String(), secret) || strings.Contains(strings.Join(f.args, " "), secret) {
		t.Fatal("credential leaked or failure ignored")
	}
	if !json.Valid(f.input) {
		t.Fatal("credential not sent as structured stdin")
	}
	entries, err := os.ReadDir(e.dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("credential recorded in evidence")
	}
	_, err = e.dependencySecret(context.Background(), observabilityNS, "lab-metrics-postgres")
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("read error exposed credential")
	}
}

func TestDependencyQueriesDoNotFakeMissingSamples(t *testing.T) {
	for _, key := range []string{"pg-up", "pg-connections", "pg-max-connections", "pg-connection-ratio", "pg-transactions", "pg-rollbacks", "pg-states", "pg-collector-errors", "kafka-brokers", "kafka-lag", "kafka-members", "kafka-requests", "kafka-errors", "dependencies-health"} {
		q, err := monitoringQuery(key)
		if err != nil || !strings.Contains(q, `namespace="`+mainNS) || strings.Contains(q, "vector(0)") || strings.Contains(q, "clamp_min") {
			t.Fatalf("unscoped or misleading %s: %q %v", key, q, err)
		}
	}
	q, _ := monitoringQuery("dependencies-health")
	for _, want := range []string{"==3", ">=0", "pg_exporter_last_scrape_error"} {
		if !strings.Contains(q, want) {
			t.Fatal("missing health guard", want)
		}
	}
}

func TestDependencyACLAndChartSources(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	b, err := os.ReadFile("cluster/kafka/monitoring-user.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "Write") || strings.Contains(string(b), "Read") || strings.Contains(string(b), "Create") {
		t.Fatal("metadata-only Kafka principal got additional privileges")
	}
	for _, file := range []string{"helm/lab-dependency-metrics/templates/exporters.yaml", "helm/lab-dependency-metrics/templates/broker.yaml"} {
		b, err = os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "NodePort") || strings.Contains(string(b), "LoadBalancer") {
			t.Fatal("public exporter exposure")
		}
	}
	if err := (monitoringEvidence{dir: t.TempDir()}).dashboardValues(); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile("cluster/observability/dashboards/dependencies.json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		UID    string
		Panels []struct {
			ID      int
			Targets []struct{ Expr string }
		}
	}
	if json.Unmarshal(b, &d) != nil || d.UID != "lab-dependency-metrics" {
		t.Fatal("invalid dependency dashboard")
	}
	seen := map[int]bool{}
	for _, p := range d.Panels {
		if seen[p.ID] {
			t.Fatal("duplicate panel ID")
		}
		seen[p.ID] = true
		for _, q := range p.Targets {
			if !strings.Contains(q.Expr, `namespace="`+mainNS) || strings.Contains(q.Expr, "vector(0)") {
				t.Fatal("unscoped or fabricated panel")
			}
		}
	}
}
