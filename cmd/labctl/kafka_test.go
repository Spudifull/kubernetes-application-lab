package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestKafkaPreflightRejectsOtherClusterOrNamespace(t *testing.T) {
	for _, tc := range []struct{ context, namespace string }{
		{netContext, mainNS}, {mainContext, policyNS},
	} {
		t.Run(tc.context+"/"+tc.namespace, func(t *testing.T) {
			f := &fakeRunner{}
			l := lab{runner: f, context: tc.context, namespace: tc.namespace, out: &bytes.Buffer{}}
			if err := l.kafkaPreflight(context.Background()); err == nil || len(f.calls) != 0 {
				t.Fatalf("wrong cluster/namespace must fail without commands: err=%v calls=%v", err, f.calls)
			}
		})
	}
}

func TestKafkaPreflightRequiresMatchingCurrentContext(t *testing.T) {
	f := &fakeRunner{replies: map[string]result{"kubectl config current-context": {out: netContext}}}
	l := lab{runner: f, context: mainContext, namespace: mainNS, out: &bytes.Buffer{}}
	if err := l.kafkaPreflight(context.Background()); err == nil || len(f.calls) != 1 {
		t.Fatalf("context refusal: err=%v calls=%v", err, f.calls)
	}
}

func kafkaPreflightFixture() *fakeRunner {
	prefix := "kubectl --context " + mainContext + " "
	return &fakeRunner{replies: map[string]result{
		"kubectl config current-context": {out: mainContext},
		"kind get clusters":              {out: "kubernetes-application-lab\nkubernetes-application-lab-net\n"},
		"docker info --format CPUs={{.NCPU}} MemoryBytes={{.MemTotal}}":            {out: "CPUs=8 MemoryBytes=17179869184"},
		prefix + "version -o json":                                                 {out: `{"serverVersion":{"gitVersion":"v1.34.0"}}`},
		prefix + "get nodes -o wide":                                               {out: "node Ready"},
		prefix + "describe nodes":                                                  {out: "Allocated resources: CPU requests 500m Memory requests 512Mi"},
		prefix + "get storageclass -o yaml":                                        {out: "reclaimPolicy: Delete"},
		prefix + "get pods --all-namespaces -o wide":                               {out: "existing workloads"},
		prefix + "get deployment lab-api --namespace " + mainNS + " -o wide":       {out: "lab-api 1/1"},
		prefix + "get statefulset lab-postgres --namespace " + mainNS + " -o wide": {out: "lab-postgres 1/1"},
		prefix + "get pvc data-lab-postgres-0 --namespace " + mainNS + " -o wide":  {out: "data-lab-postgres-0 Bound"},
		prefix + "top nodes":                                                       {out: "Metrics API not available", code: 1},
	}}
}

func TestKafkaPreflightOnlyObservesAndDoesNotApproveInstallation(t *testing.T) {
	f := kafkaPreflightFixture()
	var output bytes.Buffer
	if err := runCLI(context.Background(), []string{"kafka-preflight"}, f, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"installation_authorized":false`) ||
		!strings.Contains(output.String(), `"available":false`) ||
		!strings.Contains(output.String(), `"check":"node_capacity_and_allocations"`) {
		t.Fatalf("missing capacity/evidence boundary: %s", output.String())
	}
	for _, call := range f.calls {
		for _, mutation := range []string{" apply ", " create ", " delete ", " patch ", " upgrade ", " install "} {
			if strings.Contains(" "+call+" ", mutation) {
				t.Fatalf("preflight mutated state: %s", call)
			}
		}
	}
}

func TestKafkaPreflightDoesNotReportCompletionAfterRequiredFailure(t *testing.T) {
	f := kafkaPreflightFixture()
	f.replies["kubectl --context "+mainContext+" describe nodes"] = result{out: "connection refused", code: 1}
	var output bytes.Buffer
	l := lab{runner: f, context: mainContext, namespace: mainNS, out: &output}
	if err := l.kafkaPreflight(context.Background()); err == nil {
		t.Fatal("required evidence failure accepted")
	}
	if strings.Contains(output.String(), "kafka_preflight_completed") {
		t.Fatal("reported completed evidence collection after failure")
	}
}

func TestKafkaVersionSkew(t *testing.T) {
	for _, tc := range []struct {
		client, server string
		ok             bool
	}{{"34", "34", true}, {"35", "34", true}, {"33+", "34", true}, {"36", "34", false}, {"32", "34", false}, {"invalid", "34", false}} {
		if err := kafkaVersionSkew("1", tc.client, "1", tc.server); (err == nil) != tc.ok {
			t.Errorf("client=%s server=%s: %v", tc.client, tc.server, err)
		}
	}
}

func TestKafkaMutationCommandsRejectWrongContextBeforeChangingResources(t *testing.T) {
	for _, command := range []string{"kafka-bootstrap", "kafka-smoke", "kafka-smoke-cleanup", "kafka-broker-restart", "kafka-broker-outage", "kafka-strimzi"} {
		t.Run(command, func(t *testing.T) {
			f := &fakeRunner{replies: map[string]result{"kubectl config current-context": {out: netContext}}}
			args := []string{command}
			if command == "kafka-strimzi" {
				args = append(args, "--action", "install")
			}
			if command == "kafka-broker-outage" {
				args = append(args, "--action", "start")
			}
			if err := runCLI(context.Background(), args, f, &bytes.Buffer{}); err == nil || len(f.calls) != 1 {
				t.Fatalf("context refusal before mutation: err=%v calls=%v", err, f.calls)
			}
		})
	}
}

func TestKafkaSmokePodUsesOnlyMountedCredentialsAndBoundedResources(t *testing.T) {
	p := kafkaSmokePod("probe", "image:hash", []string{"-mode", "write", "-key", "smoke-test"})
	spec := p["spec"].(map[string]any)
	if spec["restartPolicy"] != "Never" || spec["activeDeadlineSeconds"] != 75 || spec["automountServiceAccountToken"] != false {
		t.Fatal("probe lifecycle/security limits missing")
	}
	container := spec["containers"].([]any)[0].(map[string]any)
	if _, exists := container["env"]; exists {
		t.Fatal("probe should mount credentials, not place them in env")
	}
	if len(spec["volumes"].([]any)) != 2 {
		t.Fatal("CA and user credential volumes missing")
	}
}

func TestKafkaPVCEvidenceRejectsEmptyUnboundOrIncompleteStorage(t *testing.T) {
	for _, input := range []string{
		`{"items":[]}`,
		`{"items":[{"metadata":{"name":"data","uid":"uid"},"spec":{"volumeName":"pv"},"status":{"phase":"Pending"}}]}`,
		`{"items":[{"metadata":{"name":"data"},"spec":{"volumeName":"pv"},"status":{"phase":"Bound"}}]}`,
	} {
		var list kafkaPVCList
		if err := json.Unmarshal([]byte(input), &list); err != nil {
			t.Fatal(err)
		}
		if _, err := kafkaPVCIdentity(list); err == nil {
			t.Fatalf("unsafe PVC evidence accepted: %s", input)
		}
	}
}
