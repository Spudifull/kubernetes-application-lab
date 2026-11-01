package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

type tracingChart struct{ release, chart, repository, version, values string }

var tracingCharts = []tracingChart{
	{"lab-tracing-controls", "helm/lab-tracing-controls", "", "0.1.0", ""},
	{"lab-tempo", "tempo", "https://grafana-community.github.io/helm-charts", "3.2.1", "cluster/observability/tempo-values.yaml"},
	{"lab-otel-collector", "opentelemetry-collector", "https://open-telemetry.github.io/opentelemetry-helm-charts", "0.178.1", "cluster/observability/collector-values.yaml"},
}

func tracingArgs(c tracingChart) []string {
	args := []string{c.chart, "--namespace", observabilityNS}
	if c.repository != "" {
		args = append(args, "--repo", c.repository, "--version", c.version)
	}
	if c.values != "" {
		args = append(args, "-f", c.values)
	}
	return args
}

func tracingOwnership(data []byte) error {
	var releases []struct{ Name, Namespace, Chart, Status string }
	if err := json.Unmarshal(data, &releases); err != nil {
		return err
	}
	for _, r := range releases {
		for _, c := range tracingCharts {
			chartName := c.chart
			if c.repository == "" {
				chartName = "lab-tracing-controls"
			}
			if r.Name == c.release || strings.HasPrefix(r.Chart, chartName+"-") {
				if r.Name != c.release || r.Namespace != observabilityNS || r.Chart != chartName+"-"+c.version ||
					(r.Status != "deployed" && r.Status != "failed") {
					return errors.New("tracing release owner/version/state requires review")
				}
			}
		}
	}
	return nil
}

func tracingCapacity(nodes, pods []byte) error {
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(pods, &list); err != nil {
		return err
	}
	// Actual 640Mi requests / 1280Mi limits plus 128Mi each for init/overlap.
	// On repeat, existing tracing Pods are conservatively counted as well.
	list.Items = append(list.Items, json.RawMessage(`{"spec":{"containers":[{"resources":{"requests":{"memory":"768Mi","cpu":"150m"},"limits":{"memory":"1408Mi"}}}]}}`))
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return monitoringCapacity(nodes, b)
}

func (e monitoringEvidence) tracing(ctx context.Context, action string) error {
	switch action {
	case "tracing-status":
		var failures []error
		for _, c := range tracingCharts {
			if _, err := e.capture(ctx, c.release+"-status", "helm", "status", c.release, "-n", observabilityNS, "--kube-context", e.lab.context); err != nil {
				failures = append(failures, err)
			}
		}
		_, err := e.kube(ctx, "tracing-workloads", "get", "deployment,statefulset,pod,service,pvc,servicemonitor,networkpolicy", "-n", observabilityNS, "-o", "json")
		if err == nil {
			e.lab.print("Tracing inventory saved: " + e.dir)
		}
		return errors.Join(append(failures, err)...)
	case "tracing-probe":
		return e.tracingProbe(ctx)
	}
	for _, c := range tracingCharts {
		args := append([]string{"template", c.release}, tracingArgs(c)...)
		args = append(args, "--kube-version", "1.34.0")
		out, err := e.capture(ctx, c.release+"-render", "helm", args...)
		if err != nil {
			return err
		}
		if err := e.save(c.release+".yaml", []byte(out)); err != nil {
			return err
		}
	}
	if action == "tracing-render" {
		e.lab.print("Review three rendered files, images, PVC, ports, security contexts and collectors. No resources changed.")
		return nil
	}
	if action != "tracing-install" {
		return errors.New("unsupported tracing action")
	}
	if err := e.capacity(ctx); err != nil {
		return err
	}
	if _, err := e.kube(ctx, "tracing-namespace", "get", "namespace", observabilityNS); err != nil {
		return err
	}
	for _, kind := range []string{"servicemonitors", "podmonitors"} {
		if _, err := e.kube(ctx, "tracing-crd-"+kind, "wait", "--for=condition=Established", "crd/"+kind+".monitoring.coreos.com", "--timeout=20s"); err != nil {
			return err
		}
	}
	storage, err := e.kube(ctx, "tracing-storage", "get", "storageclass", "standard", "-o", "json")
	if err != nil {
		return err
	}
	var sc struct{ Provisioner, ReclaimPolicy, VolumeBindingMode string }
	if json.Unmarshal([]byte(storage), &sc) != nil || sc.Provisioner != "rancher.io/local-path" || sc.ReclaimPolicy != "Delete" || sc.VolumeBindingMode != "WaitForFirstConsumer" {
		return errors.New("review tracing storage class before creating PVC")
	}
	releases, err := e.capture(ctx, "tracing-ownership", "helm", monitoringReleaseListArgs(e.lab.context)...)
	if err != nil {
		return err
	}
	if err := tracingOwnership([]byte(releases)); err != nil {
		return err
	}
	nodes, err := e.kube(ctx, "tracing-capacity-nodes", "get", "nodes", "-o", "json")
	if err != nil {
		return err
	}
	pods, err := e.kube(ctx, "tracing-capacity-pods", "get", "pods", "-A", "-o", "json")
	if err != nil {
		return err
	}
	if err := tracingCapacity([]byte(nodes), []byte(pods)); err != nil {
		return err
	}
	mem, err := e.capture(ctx, "tracing-host-memory", "docker", "exec", "kubernetes-application-lab-control-plane", "cat", "/proc/meminfo")
	if err != nil {
		return err
	}
	if err := monitoringAvailableMemory(mem); err != nil {
		return err
	}
	// Require the added trace budget above the ordinary monitoring reserve.
	if err := tracingAvailableMemory(mem); err != nil {
		return err
	}
	for _, c := range tracingCharts {
		if _, err := e.kube(ctx, c.release+"-dry-run", "apply", "--dry-run=server", "-f", filepath.Join(e.dir, c.release+".yaml")); err != nil {
			return err
		}
	}
	e.lab.print("Installing tracing releases only. Tempo creates a retained 1Gi PVC; no application migration or image changes. Failed resources are retained.")
	for _, c := range tracingCharts {
		args := append([]string{"upgrade", "--install", c.release}, tracingArgs(c)...)
		args = append(args, "--kube-context", e.lab.context, "--reset-values", "--wait", "--timeout", "180s")
		if err := e.helmInstall(ctx, c.release+"-install", args...); err != nil {
			return err
		}
	}
	return e.tracing(ctx, "tracing-status")
}

func tracingAvailableMemory(mem string) error {
	// Include the added tracing allowance above the existing monitoring reserve.
	for _, line := range strings.Split(mem, "\n") {
		var kb int64
		if _, err := fmt.Sscanf(line, "MemAvailable: %d kB", &kb); err == nil {
			if kb < int64(monitoringMemoryLimitMi+512+1408)*1024 {
				return errors.New("insufficient measured host memory for tracing plus existing monitoring reserve")
			}
			return nil
		}
	}
	return errors.New("MemAvailable missing for tracing")
}

// A single diagnostic OTLP span proves only Collector -> Tempo transport.
// It is explicitly not an application job trace or an SDK instrumentation test.
func (e monitoringEvidence) tracingProbe(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	var ids [24]byte
	if _, err := rand.Read(ids[:]); err != nil {
		return err
	}
	traceID, spanID := hex.EncodeToString(ids[:16]), hex.EncodeToString(ids[16:])
	start := time.Now().UTC()
	body, err := json.Marshal(map[string]any{"resourceSpans": []any{map[string]any{
		"resource": map[string]any{"attributes": []any{map[string]any{"key": "service.name", "value": map[string]string{"stringValue": "labctl-trace-probe"}}}},
		"scopeSpans": []any{map[string]any{"scope": map[string]string{"name": "labctl"}, "spans": []any{map[string]any{
			"traceId": traceID, "spanId": spanID, "name": "collector-tempo-transport-probe", "kind": 1,
			"startTimeUnixNano": fmt.Sprint(start.UnixNano()), "endTimeUnixNano": fmt.Sprint(start.Add(time.Millisecond).UnixNano()),
		}}}},
	}}})
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:14318/v1/traces", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("OTLP probe connection failed; keep Collector port-forward 14318 open")
	}
	b, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	closeErr := resp.Body.Close()
	if err := e.save("otlp-response.json", b); err != nil {
		return err
	}
	var ack struct {
		PartialSuccess struct {
			RejectedSpans json.Number
			ErrorMessage  string
		}
	}
	if readErr != nil || closeErr != nil || resp.StatusCode != http.StatusOK || json.Unmarshal(b, &ack) != nil ||
		(ack.PartialSuccess.RejectedSpans != "" && ack.PartialSuccess.RejectedSpans != "0") || ack.PartialSuccess.ErrorMessage != "" {
		return errors.New("OTLP probe was not fully accepted; inspect saved response")
	}
	if err := e.save("trace-locator.json", []byte(fmt.Sprintf(`{"trace_id":%q,"span_id":%q,"observed_at":%q}`, traceID, spanID, start.Format(time.RFC3339Nano)))); err != nil {
		return err
	}
	e.lab.print("Diagnostic trace_id=" + traceID + " span_id=" + spanID)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:13200/api/traces/"+traceID, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		r, err := client.Do(req)
		if err == nil {
			data, readErr := io.ReadAll(io.LimitReader(r.Body, 2<<20))
			closeErr := r.Body.Close()
			if r.StatusCode == http.StatusOK && readErr == nil && closeErr == nil && tracingSpanMatches(data, traceID, spanID) {
				if err := e.save("tempo-trace.json", data); err != nil {
					return err
				}
				e.lab.print("Diagnostic span found in Tempo. This probe does not verify application tracing.")
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("diagnostic span not confirmed in Tempo; keep Tempo port-forward 13200 open and inspect Collector/Tempo logs")
		case <-time.After(time.Second):
		}
	}
}

func tracingSpanMatches(data []byte, traceID, spanID string) bool {
	var document any
	if json.Unmarshal(data, &document) != nil {
		return false
	}
	identifier := func(value any, expected string) bool {
		s, ok := value.(string)
		if !ok {
			return false
		}
		if s == expected {
			return true
		}
		b, err := base64.StdEncoding.DecodeString(s)
		return err == nil && hex.EncodeToString(b) == expected
	}
	var matches func(any) bool
	matches = func(value any) bool {
		switch v := value.(type) {
		case map[string]any:
			if v["name"] == "collector-tempo-transport-probe" && identifier(v["traceId"], traceID) && identifier(v["spanId"], spanID) {
				return true
			}
			for _, child := range v {
				if matches(child) {
					return true
				}
			}
		case []any:
			for _, child := range v {
				if matches(child) {
					return true
				}
			}
		}
		return false
	}
	return matches(document)
}
