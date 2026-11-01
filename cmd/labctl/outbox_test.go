package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
)

func TestPublisherRestoreMatchesBootstrapBeforeHelm(t *testing.T) {
	for _, profile := range []string{"restore", "restore-tracing"} {
		t.Run(profile, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if err := os.Mkdir(".local-kafka", 0700); err != nil {
				t.Fatal(err)
			}
			tag := strings.Repeat("a", 64)
			ref := publisherRepository + ":" + tag
			if err := os.WriteFile(publisherRefFile, []byte(ref), 0600); err != nil {
				t.Fatal(err)
			}
			bootstrap := "lab-kafka-kafka-bootstrap.kubernetes-application-lab-kafka.svc.cluster.local:9093"
			values := "helm get values lab-publisher --all --output json --namespace " + mainNS + " --kube-context " + mainContext
			setEnv := "kubectl --context " + mainContext + " set env deployment/lab-publisher -n " + mainNS + " LAB_KAFKA_BOOTSTRAP=" + bootstrap
			upgrade := "helm upgrade --install lab-publisher helm/lab-publisher --namespace " + mainNS + " --kube-context " + mainContext + " --reset-values --set-string image.tag=" + tag + " --wait --timeout 120s"
			if profile == "restore-tracing" {
				upgrade += " -f cluster/observability/application-tracing-values.yaml"
			}
			f := &fakeRunner{replies: map[string]result{
				"kubectl config current-context": {out: mainContext},
				values:                           {out: `{"kafka":{"bootstrap":"` + bootstrap + `"}}`},
				setEnv:                           {out: "deployment.apps/lab-publisher env updated"},
				"docker image inspect --format {{.Id}} " + ref:                                                   {out: "sha256:" + tag},
				"kubectl --context " + mainContext + " get secret lab-publisher-kafka -n " + mainNS + " -o name": {out: "secret/lab-publisher-kafka"},
				"kind load docker-image " + ref + " --name kubernetes-application-lab":                           {},
				upgrade: {out: "deployed"},
			}}
			var output bytes.Buffer
			if err := (lab{runner: f, context: mainContext, namespace: mainNS, out: &output}).publisherAction(context.Background(), profile); err != nil {
				t.Fatal(err)
			}
			setIndex, upgradeIndex := -1, -1
			for i, call := range f.calls {
				if call == setEnv {
					setIndex = i
				}
				if call == upgrade {
					upgradeIndex = i
				}
			}
			if setIndex < 0 || upgradeIndex <= setIndex {
				t.Fatalf("restore order: %v", f.calls)
			}
		})
	}
}

func TestPublisherRestoreStopsBeforeUpgradeOnInvalidIntentOrSetFailure(t *testing.T) {
	values := "helm get values lab-publisher --all --output json --namespace " + mainNS + " --kube-context " + mainContext
	bootstrap := "lab-kafka-kafka-bootstrap.kubernetes-application-lab-kafka.svc.cluster.local:9093"
	setEnv := "kubectl --context " + mainContext + " set env deployment/lab-publisher -n " + mainNS + " LAB_KAFKA_BOOTSTRAP=" + bootstrap
	for _, body := range []string{`{}`, `invalid`, `{"kafka":{"bootstrap":"127.0.0.1:19093"}}`, `{"kafka":{"bootstrap":"` + bootstrap + `"}}`} {
		f := &fakeRunner{replies: map[string]result{
			"kubectl config current-context": {out: mainContext}, values: {out: body}, setEnv: {code: 1, out: "update refused"},
		}}
		var output bytes.Buffer
		err := (lab{runner: f, context: mainContext, namespace: mainNS, out: &output}).publisherAction(context.Background(), "restore")
		if err == nil {
			t.Fatal("invalid/failed restore accepted")
		}
		wantCalls := 2
		if strings.Contains(body, bootstrap) {
			wantCalls = 3
		}
		if len(f.calls) != wantCalls {
			t.Fatalf("continued after restore error: %v", f.calls)
		}
		for _, call := range f.calls {
			if strings.HasPrefix(call, "helm upgrade") {
				t.Fatal("upgrade after failed recovery")
			}
		}
	}
}

func TestRequestJobTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("unexpected request contract")
		}
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"job_id":"00000000-0000-4000-8000-000000000001","status":"accepted"}`))
	}))
	defer server.Close()
	request := jobs.Request{IdempotencyKey: "key", JobType: "lab_effect", Payload: jobs.Payload{DurationMS: 1000}}
	job, status, err := requestJob(context.Background(), server.Client(), server.URL, &request)
	if err != nil || status != 202 || !jobs.ValidID(job.ID) {
		t.Fatalf("job=%+v status=%d err=%v", job, status, err)
	}
}

func TestJobsProbeRejectsRemoteURLBeforeCommands(t *testing.T) {
	err := (lab{}).jobsProbe(context.Background(), "https://example.com", "enqueue", "", 0)
	if err == nil {
		t.Fatal("remote URL accepted")
	}
}
