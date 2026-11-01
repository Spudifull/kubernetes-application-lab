package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// kafkaPreflight only collects evidence. Capacity approval is deliberately not
// inferred from allocatable resources or a single utilization sample.
func (l lab) kafkaPreflight(ctx context.Context) error {
	if l.context != mainContext || l.namespace != mainNS {
		return errors.New("Kafka preflight requires the main laboratory context and namespace; PostgreSQL is in that cluster")
	}
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	checks := []struct {
		label string
		name  string
		args  []string
	}{
		{"kind_clusters", "kind", []string{"get", "clusters"}},
		{"docker_capacity", "docker", []string{"info", "--format", "CPUs={{.NCPU}} MemoryBytes={{.MemTotal}}"}},
		{"kubernetes_version", "kubectl", []string{"--context", l.context, "version", "-o", "json"}},
		{"nodes", "kubectl", []string{"--context", l.context, "get", "nodes", "-o", "wide"}},
		// describe includes capacity, allocatable, pressure conditions, and
		// scheduler-accounted requests/limits (including init containers).
		{"node_capacity_and_allocations", "kubectl", []string{"--context", l.context, "describe", "nodes"}},
		{"storage_classes", "kubectl", []string{"--context", l.context, "get", "storageclass", "-o", "yaml"}},
		{"existing_pods", "kubectl", []string{"--context", l.context, "get", "pods", "--all-namespaces", "-o", "wide"}},
		{"api", "kubectl", []string{"--context", l.context, "get", "deployment", "lab-api", "--namespace", l.namespace, "-o", "wide"}},
		{"postgresql", "kubectl", []string{"--context", l.context, "get", "statefulset", "lab-postgres", "--namespace", l.namespace, "-o", "wide"}},
		{"postgresql_storage", "kubectl", []string{"--context", l.context, "get", "pvc", "data-lab-postgres-0", "--namespace", l.namespace, "-o", "wide"}},
	}
	for _, check := range checks {
		out, err := l.must(ctx, check.name, nil, check.args...)
		if err != nil {
			return fmt.Errorf("Kafka preflight %s: %w", check.label, err)
		}
		if err := l.event("kafka_preflight_observation", map[string]any{
			"context": l.context, "check": check.label, "output": strings.TrimSpace(out),
		}); err != nil {
			return err
		}
	}
	// Metrics Server is optional. Its absence is recorded, never interpreted as
	// zero utilization or as permission to install a broker.
	metrics, err := l.kubeResult(ctx, "top", "nodes")
	if err != nil {
		return fmt.Errorf("collect optional node metrics: %w", err)
	}
	if err := l.event("kafka_preflight_metrics", map[string]any{
		"available": metrics.code == 0, "exit_code": metrics.code,
		"output": strings.TrimSpace(metrics.out),
	}); err != nil {
		return err
	}
	return l.event("kafka_preflight_completed", map[string]any{
		"context": l.context, "installation_authorized": false,
		"next_step": "Review capacity, allocated requests, pressure, storage and existing workloads before choosing broker/operator budgets; no resources changed",
	})
}
