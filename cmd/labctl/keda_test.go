package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestFixedWorkerRejectsCompetingScaler(t *testing.T) {
	f := &fakeRunner{replies: map[string]result{
		"kubectl --context " + mainContext + " get crd scaledobjects.keda.sh --ignore-not-found=true -o name": {out: "customresourcedefinition/scaledobjects.keda.sh"},
		"kubectl --context " + mainContext + " get scaledobjects -n " + mainNS + " -o json":                   {out: `{"items":[{"spec":{"scaleTargetRef":{"name":"lab-worker"}}}]}`},
	}}
	l := lab{runner: f, out: io.Discard, namespace: mainNS, context: mainContext}
	if err := l.requireFixedWorker(context.Background()); err == nil || !strings.Contains(err.Error(), "keda-disable") {
		t.Fatal("fixed mutation accepted competing scaler", err)
	}
}

func TestFixedWorkerWithoutKEDA(t *testing.T) {
	f := &fakeRunner{replies: map[string]result{
		"kubectl --context " + mainContext + " get crd scaledobjects.keda.sh --ignore-not-found=true -o name": {},
	}}
	l := lab{runner: f, out: io.Discard, namespace: mainNS, context: mainContext}
	if err := l.requireFixedWorker(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 {
		t.Fatal("queried missing API")
	}
}

func TestKEDAGuardsBeforeMutations(t *testing.T) {
	for _, action := range []string{"install", "enable", "disable", "load", "identity"} {
		l := lab{context: netContext, namespace: mainNS, out: io.Discard}
		if l.kedaAction(context.Background(), action) == nil {
			t.Fatalf("accepted wrong cluster for %s", action)
		}
	}
}
