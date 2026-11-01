package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	calls   []string
	replies map[string]result
}

func (f *fakeRunner) run(ctx context.Context, name string, args []string, input []byte) (result, error) {
	key := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, key)
	if err := ctx.Err(); err != nil {
		return result{}, err
	}
	r, ok := f.replies[key]
	if !ok {
		return result{}, errors.New("unexpected command: " + key)
	}
	return r, nil
}

func TestParseCPU(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
		ok   bool
	}{{"3900m", 3900, true}, {"4", 4000, true}, {"3n", 0, false}, {"9223372036854776", 0, false}} {
		got, err := parseCPU(tc.in)
		if (err == nil) != tc.ok || tc.ok && got != tc.want {
			t.Fatalf("parseCPU(%q)=(%d,%v)", tc.in, got, err)
		}
	}
}

func TestReadyPods(t *testing.T) {
	var list podList
	data := `{"items":[{"metadata":{"name":"ready"},"status":{"phase":"Running","podIP":"10.0.0.1","conditions":[{"type":"Ready","status":"True"}]}},{"metadata":{"name":"terminating","deletionTimestamp":"2026-10-18T00:00:00Z"},"status":{"phase":"Running","podIP":"10.0.0.2","conditions":[{"type":"Ready","status":"True"}]}},{"metadata":{"name":"unready"},"status":{"phase":"Running","podIP":"10.0.0.3","conditions":[{"type":"Ready","status":"False"}]}}]}`
	if err := json.Unmarshal([]byte(data), &list); err != nil {
		t.Fatal(err)
	}
	got := readyPods(list)
	if len(got) != 1 || got[0].Metadata.Name != "ready" {
		t.Fatalf("readyPods=%+v", got)
	}
}

func TestEndpointSliceConditions(t *testing.T) {
	var slices endpointSliceList
	data := `{"items":[{"metadata":{"name":"slice"},"endpoints":[{"addresses":["10.0.0.1"],"conditions":{"ready":true,"serving":true,"terminating":false},"targetRef":{"name":"api"}}]}]}`
	if err := json.Unmarshal([]byte(data), &slices); err != nil {
		t.Fatal(err)
	}
	e := slices.Items[0].Endpoints[0]
	if e.Conditions.Ready == nil || !*e.Conditions.Ready || e.Conditions.Serving == nil || !*e.Conditions.Serving || e.Conditions.Terminating == nil || *e.Conditions.Terminating {
		t.Fatalf("conditions=%+v", e.Conditions)
	}
}

func TestHPAUtilizationShape(t *testing.T) {
	var h hpa
	data := `{"status":{"currentReplicas":2,"desiredReplicas":4,"currentMetrics":[{"type":"Resource","resource":{"name":"cpu","current":{"averageUtilization":83}}}],"conditions":[{"type":"AbleToScale","status":"True","reason":"Ready"}]}}`
	if err := json.Unmarshal([]byte(data), &h); err != nil {
		t.Fatal(err)
	}
	if h.Status.DesiredReplicas != 4 || h.Status.CurrentMetrics[0].Resource.Current.AverageUtilization == nil || *h.Status.CurrentMetrics[0].Resource.Current.AverageUtilization != 83 {
		t.Fatal("HPA CPU utilization not parsed")
	}
}

func TestClientPodSecurityAndBounds(t *testing.T) {
	p := clientPod("example", mainNS, "lab", []string{"-mode=probe"}, "10m", "100m", 90)
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Spec struct {
			ActiveDeadlineSeconds        int  `json:"activeDeadlineSeconds"`
			AutomountServiceAccountToken bool `json:"automountServiceAccountToken"`
			Containers                   []struct {
				SecurityContext struct {
					AllowPrivilegeEscalation bool `json:"allowPrivilegeEscalation"`
				} `json:"securityContext"`
			} `json:"containers"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Spec.ActiveDeadlineSeconds != 90 || decoded.Spec.AutomountServiceAccountToken || decoded.Spec.Containers[0].SecurityContext.AllowPrivilegeEscalation {
		t.Fatal("unsafe client Pod")
	}
}

func TestRejectWrongContextBeforePodCreate(t *testing.T) {
	f := &fakeRunner{replies: map[string]result{"kubectl config current-context": {out: netContext}}}
	l := lab{runner: f, context: mainContext, namespace: mainNS, out: &bytes.Buffer{}}
	_, err := l.createPod(context.Background(), clientPod("x", mainNS, "x", nil, "10m", "100m", 90), false)
	if err == nil || len(f.calls) != 1 {
		t.Fatalf("expected context refusal before mutation, calls=%v err=%v", f.calls, err)
	}
}

func TestRequireContextTimeout(t *testing.T) {
	f := &fakeRunner{replies: map[string]result{}}
	l := lab{runner: f, context: mainContext, namespace: mainNS, out: &bytes.Buffer{}}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := l.requireContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestExternalCommandErrors(t *testing.T) {
	f := &fakeRunner{replies: map[string]result{"kubectl --context " + mainContext + " get pods": {out: "server unavailable", code: 17}}}
	l := lab{runner: f, context: mainContext, namespace: mainNS, out: &bytes.Buffer{}}
	if _, err := l.kube(context.Background(), "get", "pods"); err == nil || !strings.Contains(err.Error(), "exited 17") {
		t.Fatalf("unexpected exit-code handling: %v", err)
	}
	if _, err := l.kube(context.Background(), "get", "missing"); err == nil || !strings.Contains(err.Error(), "unexpected command") {
		t.Fatalf("unexpected runner-error handling: %v", err)
	}
}

func TestServerRunIDCorrelation(t *testing.T) {
	logs := "{\"msg\":\"work_started\",\"run_id\":\"other\"}\n{\"msg\":\"work_started\",\"run_id\":\"wanted\"}"
	if !hasServerEvent(logs, "work_started", "wanted") || hasServerEvent(logs, "work_completed", "wanted") || hasServerEvent(logs, "work_started", "missing") {
		t.Fatal("incorrect run ID correlation")
	}
}

func TestNoDeleteWithoutActiveMatchingWork(t *testing.T) {
	for _, tc := range []struct {
		name, logs string
		allowed    bool
	}{
		{"other_run", `{"msg":"work_started","run_id":"other"}`, false},
		{"completed", `{"msg":"work_started","run_id":"wanted"}` + "\n" + `{"msg":"work_completed","run_id":"wanted"}`, false},
		{"active", `{"msg":"work_started","run_id":"wanted"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if (canDeleteWork(tc.logs, "wanted") == nil) != tc.allowed {
				t.Fatal("wrong deletion precondition")
			}
		})
	}
}

func TestRBACRejectsUnexpectedResult(t *testing.T) {
	f := &fakeRunner{replies: map[string]result{"kubectl --context " + netContext + " auth can-i get pods/lab-rbac-client --namespace " + policyNS + " --as system:serviceaccount:" + policyNS + ":lab-rbac-reader": {out: "no\n", code: 1}}}
	l := lab{runner: f, context: netContext, namespace: policyNS, out: &bytes.Buffer{}}
	if err := l.rbacTest(context.Background()); err == nil {
		t.Fatal("unexpected RBAC denial accepted")
	}
}

func TestNetworkPolicyRejectsWrongContext(t *testing.T) {
	l := lab{runner: &fakeRunner{}, context: mainContext, namespace: policyNS, out: &bytes.Buffer{}}
	if err := l.networkPolicyTest(context.Background()); err == nil {
		t.Fatal("wrong context accepted")
	}
}

func TestImageReferenceValidation(t *testing.T) {
	good := imageRepository + ":" + strings.Repeat("a", 64)
	if _, err := validImageRef(good); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"other:" + strings.Repeat("a", 64), imageRepository + ":short", imageRepository + ":" + strings.Repeat("g", 64)} {
		if _, err := validImageRef(ref); err == nil {
			t.Fatalf("accepted %q", ref)
		}
	}
}

func TestExpectedNetworkDenials(t *testing.T) {
	for _, tc := range []struct {
		name       string
		result     result
		pass, deny bool
	}{
		{"pass", result{out: "http_code=200 remote_ip=10.0.0.1", code: 0}, true, false},
		{"denied", result{out: "http_code=000", code: 7}, false, true},
		{"timeout", result{out: "http_code=000", code: 28}, false, true},
		{"dns_error", result{out: "http_code=000", code: 6}, false, false},
		{"unexpected_http", result{out: "http_code=503", code: 0}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pass, deny := classifyHTTPProbe(tc.result)
			if pass != tc.pass || deny != tc.deny {
				t.Fatalf("class=%t,%t", pass, deny)
			}
		})
	}
}

func TestPSADenialNeedsSpecificReason(t *testing.T) {
	if !isExpectedPSADenial(result{code: 1, out: `forbidden: violates PodSecurity "restricted:latest": host namespaces (hostPID=true)`}) {
		t.Fatal("expected denial rejected")
	}
	if isExpectedPSADenial(result{code: 1, out: "invalid Pod schema"}) || isExpectedPSADenial(result{code: 0, out: `violates PodSecurity "restricted:latest": hostPID`}) {
		t.Fatal("invalid denial accepted")
	}
}
