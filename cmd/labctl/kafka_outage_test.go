package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func outageTestState() kafkaOutageState {
	return kafkaOutageState{Version: 1, Context: mainContext, Namespace: kafkaNS, StartedAt: time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC),
		OperatorUID: "operator-uid", OperatorReplicas: 1, KafkaUID: "kafka-uid", PoolUID: "pool-uid", PodSetUID: "podset-uid", BrokerUID: "broker-old", NodeID: 0,
		PVCs: map[string]string{"data-0-" + brokerName: "pvc-uid/pv-name"}}
}

func TestOutageStateIsExclusiveAndValidated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := outageTestState()
	if err := saveOutageState(path, s); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s.BrokerUID = "changed"
	if err := saveOutageState(path, s); err == nil {
		t.Fatal("overwrote active recovery evidence")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("recovery state mutated", err)
	}
	for _, change := range []func(*kafkaOutageState){
		func(s *kafkaOutageState) { s.Context = netContext }, func(s *kafkaOutageState) { s.OperatorReplicas = 2 },
		func(s *kafkaOutageState) { s.PVCs = nil }, func(s *kafkaOutageState) { s.PodSetUID = "" },
	} {
		s := outageTestState()
		change(&s)
		if s.validate() == nil {
			t.Fatal("invalid recovery state accepted")
		}
	}
}

const outageKube = "kubectl --context " + mainContext + " "

func outageTestOperator(replicas int) string {
	body, _ := json.Marshal(map[string]any{"metadata": map[string]string{"name": operatorName, "uid": "operator-uid"}, "spec": map[string]any{"replicas": replicas, "selector": map[string]any{"matchLabels": map[string]string{"name": operatorName}}}})
	return string(body)
}

type outageTestRunner struct {
	fakeRunner
	stateSavedAtScale bool
	brokerDeleted     bool
}

func (r *outageTestRunner) run(ctx context.Context, name string, args []string, input []byte) (result, error) {
	key := name + " " + strings.Join(args, " ")
	out, err := r.fakeRunner.run(ctx, name, args, input)
	if err != nil || out.code != 0 {
		return out, err
	}
	if strings.Contains(key, " scale deployment/"+operatorName) {
		_, err := os.ReadFile(kafkaOutageFile)
		r.stateSavedAtScale = err == nil
		replicas := 0
		if strings.Contains(key, "--replicas=1") {
			replicas = 1
		}
		r.replies[outageKube+"get deployment "+operatorName+" -n "+kafkaNS+" -o json"] = result{out: outageTestOperator(replicas)}
	}
	if strings.Contains(key, " delete pod "+brokerName+" ") {
		r.brokerDeleted = true
		r.replies[outageKube+"get pods -n "+kafkaNS+" -l "+brokerSelector+" -o json"] = result{out: `{"items":[]}`}
	}
	return out, nil
}
func outageFixture() *outageTestRunner {
	pod := `{"metadata":{"name":"lab-kafka-dual-role-0","uid":"broker-old","ownerReferences":[{"kind":"StrimziPodSet","name":"lab-kafka-dual-role","uid":"podset-uid"}]},"status":{"conditions":[{"type":"Ready","status":"True"}]}}`
	r := &outageTestRunner{fakeRunner: fakeRunner{replies: map[string]result{
		outageKube + "get deployment " + operatorName + " -n " + kafkaNS + " -o json":                             {out: outageTestOperator(1)},
		outageKube + "rollout status deployment/" + operatorName + " -n " + kafkaNS + " --timeout=60s":            {},
		outageKube + "rollout status deployment/" + operatorName + " -n " + kafkaNS + " --timeout=180s":           {},
		outageKube + "get kafka -n " + kafkaNS + " -o json":                                                       {out: `{"items":[{"metadata":{"name":"lab-kafka"}}]}`},
		outageKube + "get kafka lab-kafka -n " + kafkaNS + " -o json":                                             {out: `{"metadata":{"uid":"kafka-uid"}}`},
		outageKube + "get strimzipodset lab-kafka-dual-role -n " + kafkaNS + " -o json":                           {out: `{"metadata":{"uid":"podset-uid"}}`},
		outageKube + "get kafkanodepool dual-role -n " + kafkaNS + " -o json":                                     {out: `{"metadata":{"uid":"pool-uid"},"spec":{"replicas":1,"roles":["broker","controller"]},"status":{"nodeIds":[0]}}`},
		outageKube + "get pvc -n " + kafkaNS + " -l strimzi.io/cluster=lab-kafka -o json":                         {out: `{"items":[{"metadata":{"name":"data-0-lab-kafka-dual-role-0","uid":"pvc-uid"},"spec":{"volumeName":"pv-name"},"status":{"phase":"Bound"}}]}`},
		outageKube + "get pods -n " + kafkaNS + " -l " + brokerSelector + " -o json":                              {out: `{"items":[` + pod + `]}`},
		outageKube + "get pod " + brokerName + " -n " + kafkaNS + " -o json":                                      {out: pod},
		outageKube + "get pods -n " + kafkaNS + " -l name=" + operatorName + " -o json":                           {out: `{"items":[]}`},
		outageKube + "scale deployment/" + operatorName + " -n " + kafkaNS + " --current-replicas=1 --replicas=0": {},
		outageKube + "scale deployment/" + operatorName + " -n " + kafkaNS + " --current-replicas=0 --replicas=1": {},
		outageKube + "delete pod " + brokerName + " -n " + kafkaNS + " --wait=true --timeout=90s":                 {},
	}}}
	return r
}

func TestBrokerOutageSavesBeforeStoppingAndRestoresIdentities(t *testing.T) {
	t.Chdir(t.TempDir())
	r := outageFixture()
	var output bytes.Buffer
	l := lab{runner: r, context: mainContext, namespace: mainNS, out: &output}
	if err := l.startBrokerOutage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !r.stateSavedAtScale || !r.brokerDeleted {
		t.Fatal("mutation order or recovery evidence missing")
	}
	body, err := os.ReadFile(kafkaOutageFile)
	if err != nil {
		t.Fatal(err)
	}
	var s kafkaOutageState
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatal(err)
	}
	newPod := `{"items":[{"metadata":{"name":"lab-kafka-dual-role-0","uid":"broker-new"},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`
	r.replies[outageKube+"get pods -n "+kafkaNS+" -l "+brokerSelector+" -o json"] = result{out: newPod}
	if err := l.restoreBrokerOutage(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(kafkaOutageFile); !os.IsNotExist(err) {
		t.Fatal("active recovery state not archived")
	}
	if !strings.Contains(output.String(), `"broker_replaced":true`) {
		t.Fatal("replacement evidence missing")
	}
	for _, call := range r.calls {
		if strings.Contains(call, "delete pvc") || strings.Contains(call, "patch strimzipodset") {
			t.Fatalf("unexpected storage or internal controller mutation: %s", call)
		}
	}
}

func TestBrokerOutageRefusesStorageDriftBeforeRecoveryMutation(t *testing.T) {
	r := outageFixture()
	r.replies[outageKube+"get pvc -n "+kafkaNS+" -l strimzi.io/cluster=lab-kafka -o json"] = result{out: `{"items":[{"metadata":{"name":"data-0-lab-kafka-dual-role-0","uid":"different"},"spec":{"volumeName":"pv-name"},"status":{"phase":"Bound"}}]}`}
	l := lab{runner: r, context: mainContext, namespace: mainNS, out: &bytes.Buffer{}}
	if err := l.restoreBrokerOutage(context.Background(), outageTestState()); err == nil {
		t.Fatal("changed storage accepted")
	}
	for _, call := range r.calls {
		if strings.Contains(call, " scale ") || strings.Contains(call, " delete ") {
			t.Fatalf("mutation after identity mismatch: %s", call)
		}
	}
}

func TestBrokerOutageDoesNotOverwriteActiveExperiment(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(".local-kafka", 0700); err != nil {
		t.Fatal(err)
	}
	if err := saveOutageState(kafkaOutageFile, outageTestState()); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{}
	l := lab{runner: r, context: mainContext, namespace: mainNS, out: &bytes.Buffer{}}
	if err := l.startBrokerOutage(context.Background()); err == nil || len(r.calls) != 0 {
		t.Fatalf("started another outage: err=%v calls=%v", err, r.calls)
	}
}
