package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestCronRejectsInvalidReplayBeforeClusterAccess(t *testing.T) {
	for _, action := range []string{"unknown", "run"} {
		runner := &fakeRunner{}
		l := lab{runner: runner, out: io.Discard, namespace: mainNS, context: mainContext}
		if err := l.cronAction(context.Background(), action, "now"); err == nil {
			t.Fatal("invalid action or timestamp accepted")
		}
		if len(runner.calls) != 0 {
			t.Fatal("invalid replay touched cluster")
		}
	}
}

func TestCronProfileRefusesActiveJob(t *testing.T) {
	f := &fakeRunner{replies: map[string]result{
		"kubectl config current-context": {out: mainContext},
		"kubectl --context " + mainContext + " get cronjob/lab-cron -n " + mainNS + " -o json": {out: `{"spec":{"suspend":true},"status":{"active":[{"name":"running"}]}}`},
	}}
	l := lab{runner: f, out: io.Discard, namespace: mainNS, context: mainContext}
	if l.cronProfile(context.Background(), "normal") == nil {
		t.Fatal("profile changed during active Job")
	}
	for _, call := range f.calls {
		if strings.HasPrefix(call, "helm ") {
			t.Fatal("active Job allowed Helm mutation")
		}
	}
}

func TestCronCleanupDoesNotDeleteWhenSuspendFails(t *testing.T) {
	runner := &fakeRunner{replies: map[string]result{
		"kubectl config current-context": {out: mainContext},
		"helm upgrade lab-cron helm/lab-cron --namespace " + mainNS + " --kube-context " + mainContext + " --reuse-values --set suspend=true --wait --timeout 120s": {code: 1, out: "upgrade failed"},
	}}
	l := lab{runner: runner, out: io.Discard, namespace: mainNS, context: mainContext}
	if err := l.cronAction(context.Background(), "cleanup", ""); err == nil {
		t.Fatal("cleanup ignored failed suspend")
	}
	for _, call := range runner.calls {
		if strings.Contains(call, " delete ") {
			t.Fatal("cleanup deleted Jobs before scheduling was suspended")
		}
	}
}
