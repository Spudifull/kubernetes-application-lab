package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkerDiagnosticCredentialsAreSeparated(t *testing.T) {
	for _, action := range []string{"group-status", "replay"} {
		b, err := json.Marshal(workerDiagnosticPod("diag", "image", action, "00000000-0000-4000-8000-000000000001"))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		expected := "lab-worker-kafka"
		if action == "replay" {
			expected = "lab-publisher-kafka"
		}
		if !strings.Contains(s, `"secretName":"`+expected+`"`) || !strings.Contains(s, `"automountServiceAccountToken":false`) || !strings.Contains(s, `"readOnlyRootFilesystem":true`) || !strings.Contains(s, `"namespace":"`+mainNS+`"`) {
			t.Fatal("diagnostic privilege or namespace mismatch")
		}
		if action == "group-status" && strings.Contains(s, "LAB_DB_PASSWORD") {
			t.Fatal("read-only group probe got DB credentials")
		}
	}
}

func TestWorkerGuardRejectsOtherClusterBeforeCommands(t *testing.T) {
	var out bytes.Buffer
	l := lab{context: netContext, namespace: mainNS, out: &out}
	if l.workerAction(context.Background(), "deploy") == nil {
		t.Fatal("other cluster accepted")
	}
	if l.workerAction(context.Background(), "deploy-crash") == nil {
		t.Fatal("other cluster crash accepted")
	}
	if l.workerAction(context.Background(), "deploy-crash-tracing") == nil {
		t.Fatal("other cluster traced crash accepted")
	}
	if l.workerDiagnostic(context.Background(), "replay", "") == nil {
		t.Fatal("other cluster replay accepted")
	}
}

func TestWorkerCrashProfilePreservesTracingAndDisarmsOnRecovery(t *testing.T) {
	l := lab{context: mainContext, namespace: mainNS}
	for _, tc := range []struct {
		action         string
		crash, tracing bool
	}{
		{"deploy-crash", true, false}, {"deploy-crash-tracing", true, true}, {"deploy-tracing", false, true}, {"stop", false, false},
	} {
		id := ""
		if tc.crash {
			id = "00000000-0000-4000-8000-000000000001"
		}
		args := l.workerHelmArgs(tc.action, workerRepository+":"+strings.Repeat("a", 64), id)
		command := strings.Join(args, " ")
		if strings.Contains(command, "-f cluster/observability/application-tracing-values.yaml") != tc.tracing {
			t.Fatalf("wrong tracing profile: %s", command)
		}
		if strings.Contains(command, " --wait") == tc.crash {
			t.Fatalf("wrong readiness wait for crash/recovery: %s", command)
		}
		if !strings.Contains(command, "--set-string lab.crashBeforeOffsetJobID="+id+" ") {
			t.Fatalf("fault identity not explicitly set/cleared: %s", command)
		}
	}
}
func TestWorkerBatchRejectsRemoteURL(t *testing.T) {
	if (lab{}).workerBatch(context.Background(), "https://example.com") == nil {
		t.Fatal("remote batch target accepted")
	}
}

func TestReliabilityDiagnosticCredentials(t *testing.T) {
	for _, tt := range []struct {
		action, secret string
		db             bool
	}{
		{"retry-success", "", true},
		{"retry-exhaust", "", true},
		{"poison", "lab-publisher-kafka", false},
		{"dlq-read", "lab-dlq-inspector-kafka", true},
	} {
		b, err := json.Marshal(workerDiagnosticPod("diag", "image", tt.action, "00000000-0000-4000-8000-000000000001"))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if tt.secret != "" && !strings.Contains(s, `"secretName":"`+tt.secret+`"`) {
			t.Fatalf("wrong Kafka identity for %s", tt.action)
		}
		if tt.secret == "" && strings.Contains(s, "LAB_KAFKA") {
			t.Fatalf("SQL-only diagnostic received Kafka credentials for %s", tt.action)
		}
		if strings.Contains(s, "LAB_DB_PASSWORD") != tt.db {
			t.Fatalf("wrong credentials for %s", tt.action)
		}
	}
}
