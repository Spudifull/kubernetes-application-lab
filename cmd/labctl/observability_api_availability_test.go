package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestAPIAvailabilityRejectsCompetingHPA(t *testing.T) {
	f := &fakeRunner{replies: map[string]result{
		"kubectl --context " + mainContext + " get hpa -n " + mainNS + " -o json": {out: `{"items":[{"spec":{"scaleTargetRef":{"kind":"Deployment","name":"lab-api"}}}]}`},
	}}
	e := monitoringEvidence{lab: lab{context: mainContext, namespace: mainNS, runner: f, out: &bytes.Buffer{}}, dir: t.TempDir()}
	if err := e.apiAvailability(context.Background(), "api-alert-stop", ""); err == nil || !strings.Contains(err.Error(), "HPA") {
		t.Fatal("competing HPA accepted", err)
	}
	if len(f.calls) != 1 {
		t.Fatal("mutation attempted after HPA rejection", f.calls)
	}
}

func TestAPIAvailabilityRejectsKEDAAndUnexpectedReplicaCount(t *testing.T) {
	for _, competing := range []bool{true, false} {
		prefix := "kubectl --context " + mainContext + " "
		f := &fakeRunner{replies: map[string]result{
			prefix + "get hpa -n " + mainNS + " -o json":                                      {out: `{"items":[]}`},
			prefix + "get crd scaledobjects.keda.sh --ignore-not-found=true -o name":          {out: "crd/scaledobjects.keda.sh"},
			prefix + "get scaledobjects -n " + mainNS + " -o json":                            {out: `{"items":[]}`},
			prefix + "--request-timeout=30s get deployment/lab-api -n " + mainNS + " -o json": {out: `{"metadata":{"labels":{"app.kubernetes.io/instance":"lab-api"},"annotations":{"meta.helm.sh/release-name":"lab-api"}},"spec":{"replicas":2}}`},
		}}
		if competing {
			f.replies[prefix+"get scaledobjects -n "+mainNS+" -o json"] = result{out: `{"items":[{"spec":{"scaleTargetRef":{"name":"lab-api"}}}]}`}
		}
		e := monitoringEvidence{lab: lab{context: mainContext, namespace: mainNS, runner: f, out: &bytes.Buffer{}}, dir: t.TempDir()}
		err := e.apiAvailability(context.Background(), "api-alert-stop", "")
		if err == nil || (competing && !strings.Contains(err.Error(), "KEDA")) || (!competing && !strings.Contains(err.Error(), "one fixed replica")) {
			t.Fatal("unsafe pause accepted", err)
		}
		for _, call := range f.calls {
			if strings.Contains(call, " scale ") {
				t.Fatal("mutation attempted", call)
			}
		}
	}
}

func TestAPIAvailabilityServiceProxyEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, key, deployment string
		reply                 result
		ok                    bool
	}{
		{"healthy", "healthy", `{"spec":{"replicas":1},"status":{"availableReplicas":1}}`, result{out: `{"version":"0.1.0","revision":"0123456789ab"}`}, true},
		{"empty_version", "healthy", `{"spec":{"replicas":1},"status":{"availableReplicas":1}}`, result{out: `{}`}, false},
		{"unready", "healthy", `{"spec":{"replicas":1},"status":{"availableReplicas":0}}`, result{out: `{"version":"0.1.0","revision":"0123456789ab"}`}, false},
		{"no_endpoints", "unavailable", `{"spec":{"replicas":0},"status":{"availableReplicas":0}}`, result{code: 1, out: `Error from server (ServiceUnavailable): no endpoints available for service "lab-api"`}, true},
		{"control_plane_failure", "unavailable", `{"spec":{"replicas":0},"status":{"availableReplicas":0}}`, result{code: 1, out: `Unable to connect to the server`}, false},
		{"success_during_outage", "unavailable", `{"spec":{"replicas":0},"status":{"availableReplicas":0}}`, result{out: `{"version":"0.1.0","revision":"0123456789ab"}`}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix := "kubectl --context " + mainContext + " "
			f := &fakeRunner{replies: map[string]result{
				prefix + "--request-timeout=30s get deployment/lab-api -n " + mainNS + " -o json":                                   {out: tc.deployment},
				prefix + "--request-timeout=15s get --raw /api/v1/namespaces/" + mainNS + "/services/http:lab-api:80/proxy/version": tc.reply,
			}}
			e := monitoringEvidence{lab: lab{context: mainContext, namespace: mainNS, runner: f, out: &bytes.Buffer{}}, dir: t.TempDir()}
			err := e.apiAvailabilityHTTP(context.Background(), tc.key)
			if (err == nil) != tc.ok {
				t.Fatal("unexpected probe result", err)
			}
		})
	}
}
