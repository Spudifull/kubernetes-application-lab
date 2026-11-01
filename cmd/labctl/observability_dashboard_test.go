package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplicationDashboardIsProvisionedWithoutDuplicateJSON(t *testing.T) {
	// Unit-test the real source without running Helm, Kubernetes or a browser.
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	e := monitoringEvidence{dir: t.TempDir()}
	if err := e.dashboardValues(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(e.dir, "dashboard-values.json"))
	if err != nil {
		t.Fatal(err)
	}
	var values struct {
		Grafana struct {
			Dashboards map[string]map[string]struct {
				JSON string `json:"json"`
			}
		}
	}
	if err := json.Unmarshal(b, &values); err != nil {
		t.Fatal(err)
	}
	dependencyJSON, err := os.ReadFile("cluster/observability/dashboards/dependencies.json")
	if err != nil || values.Grafana.Dashboards["lab"]["dependencies"].JSON != string(dependencyJSON) {
		t.Fatal("dependency dashboard not provisioned from its canonical source", err)
	}
	var dashboard struct {
		UID    string
		Panels []struct {
			ID      int
			Targets []struct{ Expr string }
		}
	}
	if err := json.Unmarshal([]byte(values.Grafana.Dashboards["lab"]["application"].JSON), &dashboard); err != nil {
		t.Fatal(err)
	}
	if dashboard.UID != "lab-application-metrics" || len(dashboard.Panels) < 8 {
		t.Fatal("missing dashboard panels")
	}
	seen := map[int]bool{}
	for _, p := range dashboard.Panels {
		if seen[p.ID] {
			t.Fatal("duplicate panel id")
		}
		seen[p.ID] = true
		for _, target := range p.Targets {
			if !strings.Contains(target.Expr, `namespace="`+mainNS+`"`) {
				t.Fatal("unscoped panel query")
			}
			if strings.Contains(target.Expr, "kafka_consumergroup") || strings.Contains(target.Expr, "pg_up") || strings.Contains(target.Expr, "vector(1)") {
				t.Fatal("panel claims unimplemented dependency metrics")
			}
		}
	}
}

func TestApplicationMetricQueriesAreScopedAndBounded(t *testing.T) {
	for _, key := range []string{"publisher", "publisher-errors", "worker", "worker-errors", "worker-retries", "worker-duplicates", "worker-dlq", "outbox", "outbox-age", "outbox-health", "outbox-freshness", "api-p95"} {
		q, err := monitoringQuery(key)
		if err != nil || !strings.Contains(q, `namespace="`+mainNS+`"`) {
			t.Fatalf("%s: %q %v", key, q, err)
		}
	}
}
