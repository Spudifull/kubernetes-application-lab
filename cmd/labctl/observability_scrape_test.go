package main

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestMonitoringScrapeReleaseGuard(t *testing.T) {
	for _, tc := range []struct {
		name, chart, namespace, status string
		ok                             bool
	}{
		{"previous chart", "lab-observability-0.1.0", observabilityNS, "deployed", true},
		{"current chart", "lab-observability-0.2.0", observabilityNS, "deployed", true},
		{"failed release can recover", "lab-observability-0.2.0", observabilityNS, "failed", true},
		{"pending release", "lab-observability-0.2.0", observabilityNS, "pending-upgrade", false},
		{"foreign chart", "other-0.2.0", observabilityNS, "deployed", false},
		{"metrics chart", "lab-observability-0.3.0", observabilityNS, "deployed", true},
		{"future chart", "lab-observability-0.4.0", observabilityNS, "deployed", false},
		{"other namespace", "lab-observability-0.2.0", mainNS, "deployed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := `[{"name":"lab-observability","namespace":"` + tc.namespace + `","chart":"` + tc.chart + `","status":"` + tc.status + `"}]`
			if err := monitoringScrapeRelease([]byte(data)); (err == nil) != tc.ok {
				t.Fatalf("unexpected release guard result: %v", err)
			}
		})
	}
	for _, data := range []string{`[]`, `invalid`} {
		if monitoringScrapeRelease([]byte(data)) == nil {
			t.Fatal("accepted missing or invalid release")
		}
	}
}

func TestScrapeProfilesOnlyUpgradeMonitorsAfterDryRun(t *testing.T) {
	for _, action := range []string{"scrape-render", "scrape-fail", "scrape-restore"} {
		t.Run(action, func(t *testing.T) {
			dir := t.TempDir()
			setting := "apiScrapeFailure=true"
			if action == "scrape-restore" {
				setting = "apiScrapeFailure=false"
			}
			render := "helm template lab-observability helm/lab-observability --namespace " + observabilityNS + " --kube-version 1.34.0 --set " + setting
			dryRun := "kubectl --context " + mainContext + " --request-timeout=30s apply --dry-run=server -f " + filepath.Join(dir, "scrape-rendered.yaml")
			upgrade := "helm upgrade lab-observability helm/lab-observability --namespace " + observabilityNS + " --kube-context " + mainContext + " --reuse-values --set " + setting + " --wait --timeout 60s"
			f := &fakeRunner{replies: map[string]result{
				"git rev-parse HEAD": {out: "test-revision"},
				"git status --short": {},
				"helm " + strings.Join(monitoringReleaseListArgs(mainContext), " "):                                       {out: `[{"name":"lab-observability","namespace":"` + observabilityNS + `","chart":"lab-observability-0.1.0","status":"deployed"}]`},
				"helm get values lab-observability -n " + observabilityNS + " --kube-context " + mainContext + " -o json": {out: `null`},
				render:  {out: "apiVersion: monitoring.coreos.com/v1\nkind: ServiceMonitor\n"},
				dryRun:  {},
				upgrade: {},
				"kubectl --context " + mainContext + " --request-timeout=30s get servicemonitor lab-api -n " + observabilityNS + " -o json": {out: `{}`},
			}}
			e := monitoringEvidence{lab: lab{runner: f, context: mainContext, out: io.Discard}, dir: dir}
			if err := e.scrapeProfile(context.Background(), action); err != nil {
				t.Fatal(err)
			}
			if action == "scrape-render" {
				if len(f.calls) != 1 || f.calls[0] != render {
					t.Fatal("render changed resources")
				}
				return
			}
			if len(f.calls) != 8 || f.calls[5] != dryRun || f.calls[6] != upgrade {
				t.Fatalf("unexpected mutation sequence: %v", f.calls)
			}
			f.calls = nil
			f.replies[dryRun] = result{code: 1, out: "admission rejected"}
			if err := e.scrapeProfile(context.Background(), action); err == nil {
				t.Fatal("continued after rejected server dry-run")
			}
			if len(f.calls) != 6 || f.calls[5] != dryRun {
				t.Fatalf("mutated release after rejected dry-run: %v", f.calls)
			}
			f.calls = nil
			valuesCommand := "helm get values lab-observability -n " + observabilityNS + " --kube-context " + mainContext + " -o json"
			f.replies[valuesCommand] = result{out: `{"applicationNamespace":"other-application"}`}
			if err := e.scrapeProfile(context.Background(), action); err == nil {
				t.Fatal("accepted another application namespace")
			}
			if len(f.calls) != 4 {
				t.Fatalf("continued after namespace mismatch: %v", f.calls)
			}
		})
	}
}

func TestAPIUpQueryIsScoped(t *testing.T) {
	q, err := monitoringQuery("api-up")
	if err != nil || q != `up{namespace="`+mainNS+`",service="lab-api"}` {
		t.Fatalf("unexpected query %q: %v", q, err)
	}
}
