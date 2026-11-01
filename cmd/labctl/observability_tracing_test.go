package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestTracingActionsRejectOtherClusters(t *testing.T) {
	for _, action := range []string{"tracing-render", "tracing-install", "tracing-status", "tracing-probe", "forward-tempo", "forward-collector"} {
		f := &fakeRunner{}
		l := lab{context: netContext, namespace: mainNS, runner: f}
		if l.observabilityAction(context.Background(), action, "") == nil || len(f.calls) != 0 {
			t.Fatalf("unguarded tracing action %s", action)
		}
	}
}

func TestTracingRejectsForeignRelease(t *testing.T) {
	for _, tc := range []struct {
		data string
		pass bool
	}{
		{`[]`, true},
		{`[{"Name":"lab-tempo","Namespace":"` + observabilityNS + `","Chart":"tempo-3.2.1","Status":"deployed"}]`, true},
		{`[{"Name":"lab-tempo","Namespace":"other","Chart":"tempo-3.2.1","Status":"deployed"}]`, false},
		{`[{"Name":"other","Namespace":"` + observabilityNS + `","Chart":"tempo-3.2.1","Status":"deployed"}]`, false},
		{`[{"Name":"lab-tempo","Namespace":"` + observabilityNS + `","Chart":"tempo-3.2.0","Status":"deployed"}]`, false},
		{`[{"Name":"lab-tempo","Namespace":"` + observabilityNS + `","Chart":"tempo-3.2.1","Status":"pending-upgrade"}]`, false},
	} {
		if err := tracingOwnership([]byte(tc.data)); (err == nil) != tc.pass {
			t.Fatalf("%s: %v", tc.data, err)
		}
	}
}

func TestTracingCapacityReservesNewComponents(t *testing.T) {
	nodes := []byte(`{"items":[{"status":{"allocatable":{"memory":"3Gi","cpu":"12"},"conditions":[{"type":"Ready","status":"True"}]}}]}`)
	pods := []byte(`{"items":[]}`)
	if err := monitoringCapacity(nodes, pods); err != nil {
		t.Fatal(err)
	}
	if tracingCapacity(nodes, pods) == nil {
		t.Fatal("tracing reservation ignored")
	}
	if tracingAvailableMemory("MemAvailable: 4063231 kB\n") == nil {
		t.Fatal("accepted host below 3968Mi boundary")
	}
	if err := tracingAvailableMemory("MemAvailable: 4063232 kB\n"); err != nil {
		t.Fatal(err)
	}
}

func TestTracingProbeRequiresActualIdentifiers(t *testing.T) {
	traceID, spanID := "1234567890abcdef1234567890abcdef", "1234567890abcdef"
	for _, encoded := range []bool{false, true} {
		tr, sp := traceID, spanID
		if encoded {
			tb, _ := hex.DecodeString(tr)
			sb, _ := hex.DecodeString(sp)
			tr, sp = base64.StdEncoding.EncodeToString(tb), base64.StdEncoding.EncodeToString(sb)
		}
		data, err := json.Marshal(map[string]any{"batches": []any{map[string]any{"spans": []any{map[string]string{
			"name": "collector-tempo-transport-probe", "traceId": tr, "spanId": sp,
		}}}}})
		if err != nil {
			t.Fatal(err)
		}
		if !tracingSpanMatches(data, traceID, spanID) || tracingSpanMatches(data, "00000000000000000000000000000000", spanID) {
			t.Fatal("trace probe failed to distinguish returned trace IDs")
		}
	}
	if tracingSpanMatches([]byte(`{"name":"collector-tempo-transport-probe"}`), traceID, spanID) {
		t.Fatal("span name without IDs accepted")
	}
}
