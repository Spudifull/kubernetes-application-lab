package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
)

// This changes only the existing monitors release, never the monitoring stack
// or application. An interrupted experiment is restored explicitly, not by an
// automatic cleanup that would erase the failure before it can be observed.
func (e monitoringEvidence) scrapeProfile(ctx context.Context, action string) error {
	fail := action != "scrape-restore"
	if action != "scrape-render" {
		for _, c := range []struct {
			label string
			args  []string
		}{
			{"git-head", []string{"rev-parse", "HEAD"}},
			{"git-status", []string{"status", "--short"}},
		} {
			if _, err := e.capture(ctx, c.label, "git", c.args...); err != nil {
				return err
			}
		}
		out, err := e.capture(ctx, "scrape-release-ownership", "helm", monitoringReleaseListArgs(e.lab.context)...)
		if err != nil {
			return err
		}
		if err := monitoringScrapeRelease([]byte(out)); err != nil {
			return err
		}
		out, err = e.capture(ctx, "scrape-existing-values", "helm", "get", "values", "lab-observability", "-n", observabilityNS, "--kube-context", e.lab.context, "-o", "json")
		if err != nil {
			return err
		}
		var values struct{ ApplicationNamespace string }
		if err := json.Unmarshal([]byte(out), &values); err != nil {
			return err
		}
		if values.ApplicationNamespace != "" && values.ApplicationNamespace != mainNS {
			return errors.New("scrape experiment refuses a different application namespace")
		}
	}
	setting := "apiScrapeFailure=" + strconv.FormatBool(fail)
	args := []string{"template", "lab-observability", "helm/lab-observability", "--namespace", observabilityNS, "--kube-version", "1.34.0", "--set", setting}
	out, err := e.capture(ctx, "scrape-render", "helm", args...)
	if err != nil {
		return err
	}
	path := filepath.Join(e.dir, "scrape-rendered.yaml")
	if err := e.save("scrape-rendered.yaml", []byte(out)); err != nil {
		return err
	}
	if action == "scrape-render" {
		e.lab.print("Review scrape-rendered.yaml: only the API ServiceMonitor protocol differs; no resources changed.")
		return nil
	}
	if _, err := e.kube(ctx, "scrape-server-dry-run", "apply", "--dry-run=server", "-f", path); err != nil {
		return err
	}
	// Reuse preserves existing monitor settings; only the lab protocol flag changes.
	if err := e.helmInstall(ctx, "scrape-upgrade", "upgrade", "lab-observability", "helm/lab-observability", "--namespace", observabilityNS,
		"--kube-context", e.lab.context, "--reuse-values", "--set", setting, "--wait", "--timeout", "60s"); err != nil {
		return err
	}
	_, err = e.kube(ctx, "scrape-monitor-after", "get", "servicemonitor", "lab-api", "-n", observabilityNS, "-o", "json")
	if err != nil {
		return err
	}
	e.lab.print("Profile applied; Helm success is not scrape evidence. Observe targets and QUERY=api-up after reconciliation. Restore with task observability-scrape-restore.")
	return nil
}

func monitoringScrapeRelease(data []byte) error {
	var releases []struct{ Name, Namespace, Chart, Status string }
	if err := json.Unmarshal(data, &releases); err != nil {
		return err
	}
	for _, r := range releases {
		if r.Name != "lab-observability" || r.Namespace != observabilityNS {
			continue
		}
		if (r.Chart == "lab-observability-0.1.0" || r.Chart == "lab-observability-0.2.0" || r.Chart == "lab-observability-0.3.0") && (r.Status == "deployed" || r.Status == "failed") {
			return nil
		}
		return errors.New("scrape experiment requires a known lab-observability chart and a non-pending release")
	}
	return errors.New("install the application monitoring release before the scrape experiment")
}
