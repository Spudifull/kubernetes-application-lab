package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const loggingTestJob = "12345678-1234-4123-8123-123456789abc"

func loggingTestRows() []loggingRow {
	var rows []loggingRow
	for i, event := range []string{"job_accepted", "outbox_broker_ack", "worker_received", "worker_effect_committed", "worker_offset_committed"} {
		service := "lab-worker"
		if i == 0 {
			service = "lab-api"
		} else if i == 1 {
			service = "lab-publisher"
		}
		rows = append(rows, loggingRow{Time: time.Date(2026, 11, 1, 12, 0, i, 0, time.UTC), Event: event,
			Service: service, Pod: service + "-one", JobID: loggingTestJob,
			TraceID: strings.Repeat("a", 32), SpanID: strings.Repeat("b", 16), EventID: loggingTestJob})
	}
	return rows
}

func TestLoggingCorrelationRejectsIncompleteAndConflictingEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func([]loggingRow) []loggingRow
	}{
		{"missing_commit", func(r []loggingRow) []loggingRow { return r[:4] }},
		{"other_trace", func(r []loggingRow) []loggingRow { r[2].TraceID = strings.Repeat("c", 32); return r }},
		{"missing_span", func(r []loggingRow) []loggingRow { r[3].SpanID = ""; return r }},
		{"zero_trace", func(r []loggingRow) []loggingRow { r[0].TraceID = strings.Repeat("0", 32); return r }},
		{"other_event", func(r []loggingRow) []loggingRow { r[2].EventID = "12345678-1234-4123-8123-123456789abd"; return r }},
		{"wrong_service", func(r []loggingRow) []loggingRow { r[1].Service = "lab-api"; return r }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if loggingCorrelation(tc.edit(loggingTestRows()), false) == nil {
				t.Fatal("incomplete/conflicting logs accepted as correlation")
			}
		})
	}
	if err := loggingCorrelation(loggingTestRows(), false); err != nil {
		t.Fatal(err)
	}
}

func TestLoggingReplayDoesNotProveTwoRetries(t *testing.T) {
	rows := loggingTestRows()
	retry := rows[2]
	retry.Event = "worker_retry_scheduled"
	rows = append(rows, retry, retry)
	if loggingCorrelation(rows, true) == nil {
		t.Fatal("duplicate delivery accepted as two retries")
	}
	retry.Time = retry.Time.Add(time.Second)
	rows = append(rows, retry)
	if err := loggingCorrelation(rows, true); err != nil {
		t.Fatal(err)
	}
}

func loggingTestResponse(t *testing.T, row loggingRow, labels map[string]string) []byte {
	t.Helper()
	line, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{"status": "success", "data": map[string]any{"resultType": "streams",
		"result": []any{map[string]any{"stream": labels, "values": [][]string{{"1793534400000000000", string(line)}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLoggingRawResponseChecksIndexLabelsAndIdentity(t *testing.T) {
	row := loggingTestRows()[0]
	labels := func() map[string]string {
		return map[string]string{"namespace": mainNS, "service": row.Service, "pod": row.Pod, "container": "api"}
	}
	if rows, err := loggingRows(loggingTestResponse(t, row, labels()), loggingTestJob); err != nil || len(rows) != 1 {
		t.Fatalf("valid log rejected: %v", err)
	}
	for _, forbidden := range []string{"job_id", "event_id", "trace_id", "request_id", "level"} {
		indexed := labels()
		indexed[forbidden] = "value"
		if _, err := loggingRows(loggingTestResponse(t, row, indexed), loggingTestJob); err == nil {
			t.Fatalf("indexed %s accepted", forbidden)
		}
	}
	indexed := labels()
	indexed["namespace"] = "other"
	if _, err := loggingRows(loggingTestResponse(t, row, indexed), loggingTestJob); err == nil {
		t.Fatal("foreign namespace accepted")
	}
	row.JobID = ""
	if rows, err := loggingRows(loggingTestResponse(t, row, labels()), loggingTestJob); err != nil || len(rows) != 0 {
		t.Fatal("raw text match treated as job correlation")
	}
}

func TestLoggingActionsGuardClusterAndOwnership(t *testing.T) {
	for _, action := range []string{"logging-render", "logging-install", "logging-status", "logging-query", "logging-verify", "logging-retry-verify"} {
		f := &fakeRunner{}
		l := lab{context: netContext, namespace: mainNS, runner: f}
		if l.observabilityAction(context.Background(), action, "") == nil || len(f.calls) != 0 {
			t.Fatalf("unguarded logging action %s", action)
		}
	}
	for _, data := range []string{
		`[{"Name":"lab-logging","Namespace":"other","Chart":"lab-logging-0.1.0","Status":"deployed"}]`,
		`[{"Name":"other","Namespace":"` + observabilityNS + `","Chart":"lab-logging-0.1.0","Status":"deployed"}]`,
		`[{"Name":"lab-logging","Namespace":"` + observabilityNS + `","Chart":"lab-logging-0.2.0","Status":"deployed"}]`,
	} {
		if loggingOwnership([]byte(data)) == nil {
			t.Fatal("foreign release/version accepted")
		}
	}
	if err := loggingOwnership([]byte(`[]`)); err != nil {
		t.Fatal(err)
	}
}
