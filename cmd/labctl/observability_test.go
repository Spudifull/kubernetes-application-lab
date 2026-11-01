package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestObservabilityRejectsOtherClustersBeforeCommands(t *testing.T) {
	for _, action := range []string{"install", "render", "preflight", "targets", "forward-grafana", "scrape-render", "scrape-fail", "scrape-restore", "application-metrics-verify"} {
		l := lab{context: netContext, namespace: mainNS, out: io.Discard}
		if l.observabilityAction(context.Background(), action, "") == nil {
			t.Fatalf("accepted %s in another cluster", action)
		}
	}
}

func TestMonitoringOwnershipRejectsExistingForeignCRDs(t *testing.T) {
	f := &fakeRunner{replies: map[string]result{
		"helm list --all-namespaces --deployed --failed --pending --uninstalled --uninstalling --superseded --kube-context " + mainContext + " -o json": {out: "[]"},
		"kubectl --context " + mainContext + " --request-timeout=30s get crd -o json":                                                                   {out: `{"items":[{"metadata":{"name":"servicemonitors.monitoring.coreos.com"}}]}`},
	}}
	e := monitoringEvidence{lab: lab{runner: f, context: mainContext}, dir: t.TempDir()}
	if err := e.ownership(context.Background()); err == nil || !strings.Contains(err.Error(), "owner/version") {
		t.Fatal("foreign CRD accepted", err)
	}
}

func TestMonitoringOwnershipRejectsImplicitChartUpgrade(t *testing.T) {
	f := &fakeRunner{replies: map[string]result{
		"helm list --all-namespaces --deployed --failed --pending --uninstalled --uninstalling --superseded --kube-context " + mainContext + " -o json": {out: `[{"name":"lab-monitoring","namespace":"` + observabilityNS + `","chart":"kube-prometheus-stack-1.0.0"}]`},
	}}
	e := monitoringEvidence{lab: lab{runner: f, context: mainContext}, dir: t.TempDir()}
	if err := e.ownership(context.Background()); err == nil {
		t.Fatal("implicit CRD upgrade accepted")
	}
	if len(f.calls) != 1 {
		t.Fatal("continued after detecting incompatible release")
	}
}

func TestMonitoringCapacity(t *testing.T) {
	node := func(memory, pressure string) []byte {
		return []byte(`{"items":[{"status":{"allocatable":{"memory":"` + memory + `","cpu":"12"},"conditions":[{"type":"Ready","status":"True"},{"type":"MemoryPressure","status":"` + pressure + `"}]}}]}`)
	}
	pods := []byte(`{"items":[{"status":{"phase":"Running"},"spec":{"containers":[{"resources":{"requests":{"memory":"3Gi","cpu":"2500m"},"limits":{"memory":"6Gi"}}}]}}]}`)
	for _, tc := range []struct {
		name, memory, pressure string
		ok                     bool
	}{
		{"enough", "8448Mi", "False", true}, {"limits too close", "7Gi", "False", false},
		{"previous Grafana budget no longer fits", "7936Mi", "False", false},
		{"one Mi below new limit boundary", "8447Mi", "False", false},
		{"pressure", "8Gi", "True", false}, {"unknown", "bad", "False", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := monitoringCapacity(node(tc.memory, tc.pressure), pods)
			if (err == nil) != tc.ok {
				t.Fatalf("unexpected capacity result: %v", err)
			}
		})
	}
	requestPods := []byte(`{"items":[{"spec":{"containers":[{"resources":{"requests":{"memory":"700Mi"}}}]}}]}`)
	if monitoringCapacity(node("2200Mi", "False"), requestPods) == nil {
		t.Fatal("accepted old request budget after Grafana memory increase")
	}
	if err := monitoringCapacity(node("2620Mi", "False"), requestPods); err != nil {
		t.Fatal("rejected exact request boundary", err)
	}
	// A high init reservation must not disappear behind small app containers.
	initPods := []byte(`{"items":[{"spec":{"containers":[{"resources":{"requests":{"memory":"32Mi"}}}],"initContainers":[{"resources":{"requests":{"memory":"8Gi"}}}]}}]}`)
	if monitoringCapacity(node("8Gi", "False"), initPods) == nil {
		t.Fatal("ignored init resources")
	}
	if monitoringCapacity([]byte(`{"items":[]}`), pods) == nil {
		t.Fatal("accepted missing node")
	}
}

func TestMonitoringGrafanaMemoryProfile(t *testing.T) {
	// Pin the reviewed delta in values and the conservative preflight budget.
	// This is a source-level regression guard, not a Helm render or runtime test.
	data, err := os.ReadFile(filepath.Join("..", "..", "cluster", "observability", "monitoring-values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	_, section, ok := strings.Cut(text, "\ngrafana:\n")
	if !ok {
		t.Fatal("Grafana values missing")
	}
	section, _, ok = strings.Cut(section, "\nkube-state-metrics:\n")
	if !ok {
		t.Fatal("Grafana section boundary missing")
	}
	for _, want := range []string{
		"    requests: {cpu: 50m, memory: 768Mi}",
		"    limits: {cpu: 300m, memory: 1Gi}",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("review Grafana memory budget after profile change: missing %q", want)
		}
	}
	if monitoringMemoryRequestMi != 1408 || monitoringMemoryLimitMi != 2048 {
		t.Fatal("review conservative budget together with Grafana resources")
	}
}

func TestMonitoringMemoryMeasurementRequired(t *testing.T) {
	for _, tc := range []struct {
		data string
		ok   bool
	}{
		{"MemAvailable: 3891076 kB\n", true},
		{"MemAvailable: 100000 kB\n", false},
		{"MemAvailable: 2097151 kB\n", false},
		{"MemAvailable: 2097152 kB\n", false},
		{"MemAvailable: 2359295 kB\n", false},
		{"MemAvailable: 2359296 kB\n", false},
		{"MemAvailable: 2621439 kB\n", false},
		{"MemAvailable: 2621440 kB\n", true},
		{"MemFree: 8000000 kB\n", false},
		{"MemAvailable: 9000000000000000000 kB\n", false},
	} {
		if err := monitoringAvailableMemory(tc.data); (err == nil) != tc.ok {
			t.Fatalf("%q: %v", tc.data, err)
		}
	}
}

func TestMonitoringUpgradeDoesNotDoubleCountOperatorPods(t *testing.T) {
	nodes := []byte(`{"items":[{"status":{"allocatable":{"memory":"8512Mi","cpu":"12"},"conditions":[{"type":"Ready","status":"True"}]}}]}`)
	for _, tc := range []struct {
		name, instance, namespace, owner string
		controller, ok                   bool
	}{
		{"own Prometheus", "lab-monitoring-prometheus", observabilityNS, "prometheus-lab-monitoring-prometheus", true, true},
		{"own Alertmanager", "lab-monitoring-alertmanager", observabilityNS, "alertmanager-lab-monitoring-alertmanager", true, true},
		{"foreign instance", "other-prometheus", observabilityNS, "prometheus-lab-monitoring-prometheus", true, false},
		{"foreign namespace", "lab-monitoring-prometheus", mainNS, "prometheus-lab-monitoring-prometheus", true, false},
		{"foreign owner", "lab-monitoring-prometheus", observabilityNS, "other-statefulset", true, false},
		{"non-controller reference", "lab-monitoring-prometheus", observabilityNS, "prometheus-lab-monitoring-prometheus", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := map[string]any{"namespace": tc.namespace, "labels": map[string]string{"app.kubernetes.io/instance": tc.instance, "app.kubernetes.io/managed-by": "prometheus-operator"}, "ownerReferences": []any{map[string]any{"kind": "StatefulSet", "name": tc.owner, "controller": tc.controller}}}
			data, err := json.Marshal(map[string]any{"items": []any{
				map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"resources": map[string]any{"requests": map[string]string{"memory": "2978Mi"}, "limits": map[string]string{"memory": "6022Mi"}}}}}},
				map[string]any{"metadata": meta, "spec": map[string]any{"containers": []any{map[string]any{"resources": map[string]any{"requests": map[string]string{"memory": "272Mi"}, "limits": map[string]string{"memory": "560Mi"}}}}}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if err := monitoringCapacity(nodes, data); (err == nil) != tc.ok {
				t.Fatalf("capacity result: %v", err)
			}
		})
	}
}

func TestMonitoringRequiresHealthyBaseline(t *testing.T) {
	if monitoringBaseline([]byte(`{"items":[]}`)) == nil {
		t.Fatal("missing baseline accepted")
	}
	data := []byte(`{"items":[{"metadata":{"name":"lab-api","generation":2},"spec":{"replicas":1},"status":{"readyReplicas":0,"observedGeneration":2}}]}`)
	if err := monitoringBaseline(data); err == nil || !strings.Contains(err.Error(), "lab-api readiness") {
		t.Fatal("unready API accepted", err)
	}
}

func TestMonitoringEmptySamplesAreFailure(t *testing.T) {
	f := &fakeRunner{replies: map[string]result{
		"kubectl --context " + mainContext + " --request-timeout=30s get --raw /api/v1/namespaces/" + observabilityNS + "/services/http:lab-monitoring-prometheus:9090/proxy/api/v1/query?query=up": {out: `{"status":"success","data":{"result":[]}}`},
	}}
	e := monitoringEvidence{lab: lab{runner: f, context: mainContext, out: io.Discard}, dir: t.TempDir()}
	if e.query(context.Background(), "up") == nil {
		t.Fatal("absence of samples reported as success")
	}
	if _, err := monitoringQuery("../../secret"); err == nil {
		t.Fatal("unbounded query key accepted")
	}
}

// The runner sees credentials only on stdin, never in args or evidence output.
type grafanaSecretRunner struct {
	input []byte
	calls int
}

func (r *grafanaSecretRunner) run(_ context.Context, name string, args []string, input []byte) (result, error) {
	r.calls++
	if r.calls == 1 {
		return result{}, nil
	}
	if name != "kubectl" || strings.Join(args, " ") != "--context "+mainContext+" create -f -" {
		return result{code: 1}, nil
	}
	r.input = append([]byte(nil), input...)
	return result{out: "secret/lab-grafana-admin created"}, nil
}

func TestGrafanaCredentialsStayOutOfEvidence(t *testing.T) {
	r := &grafanaSecretRunner{}
	var output bytes.Buffer
	dir := t.TempDir()
	e := monitoringEvidence{lab: lab{runner: r, context: mainContext, out: &output}, dir: dir}
	if err := e.grafanaSecret(context.Background()); err != nil {
		t.Fatal(err)
	}
	var secret struct {
		StringData map[string]string `json:"stringData"`
	}
	if err := json.Unmarshal(r.input, &secret); err != nil {
		t.Fatal(err)
	}
	password := secret.StringData["admin-password"]
	if len(password) < 40 || secret.StringData["admin-user"] != "lab-admin" {
		t.Fatal("invalid dedicated credentials")
	}
	if strings.Contains(output.String(), password) {
		t.Fatal("password printed")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatal("Secret operation wrote local evidence")
	}
}
