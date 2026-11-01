package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestTempoIdentifierEncoding(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	b, _ := hex.DecodeString(id)
	if traceIdentifier(base64.StdEncoding.EncodeToString(b), 16) != id || traceIdentifier("invalid", 16) != "" {
		t.Fatal("Tempo base64 identifiers not handled")
	}
}

func TestApplicationTraceRequiresConnectedParentsAndIntervals(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	type item struct{ service, name, span, parent, start, end string }
	items := []item{
		{"lab-api", "POST /jobs", "0000000000000001", "", "100", "200"},
		{"lab-api", "jobs.enqueue.transaction", "0000000000000002", "0000000000000001", "110", "180"},
		{"lab-publisher", "outbox.publish_attempt", "0000000000000003", "0000000000000002", "300", "400"},
		{"lab-publisher", "lab.jobs.v1 publish", "0000000000000004", "0000000000000003", "320", "370"},
		{"lab-worker", "lab.jobs.v1 process", "0000000000000005", "0000000000000004", "500", "900"},
		{"lab-worker", "jobs.complete.transaction", "0000000000000006", "0000000000000005", "700", "800"},
		{"lab-worker", "lab-workers commit", "0000000000000007", "0000000000000005", "810", "850"},
	}
	encode := func() []byte {
		var batches []any
		for _, i := range items {
			batches = append(batches, map[string]any{
				"resource":   map[string]any{"attributes": []any{map[string]any{"key": "service.name", "value": map[string]string{"stringValue": i.service}}}},
				"scopeSpans": []any{map[string]any{"spans": []any{map[string]string{"traceId": id, "spanId": i.span, "parentSpanId": i.parent, "name": i.name, "startTimeUnixNano": i.start, "endTimeUnixNano": i.end}}}},
			})
		}
		b, err := json.Marshal(map[string]any{"batches": batches})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if chain, err := applicationTraceChain(encode(), id); err != nil || len(chain) != 7 {
		t.Fatal("valid async path rejected", err)
	}
	items[4].parent = "0000000000000001"
	if _, err := applicationTraceChain(encode(), id); err == nil {
		t.Fatal("disconnected consumer accepted")
	}
	items[4].parent = "0000000000000004"
	items[6].start = "750"
	if _, err := applicationTraceChain(encode(), id); err == nil {
		t.Fatal("offset-before-SQL accepted")
	}
}

func TestTraceInspectionKeepsPartialCrashEvidenceWithoutSuccessClaim(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	encode := func(spans []applicationSpan) []byte {
		b, err := json.Marshal(map[string]any{"batches": []any{map[string]any{
			"resource":   map[string]any{"attributes": []any{map[string]any{"key": "service.name", "value": map[string]string{"stringValue": "lab-worker"}}}},
			"scopeSpans": []any{map[string]any{"spans": spans}},
		}}})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	s := applicationSpan{TraceID: id, SpanID: "0000000000000001", ParentID: "0000000000000002", Name: "jobs.start.transaction", Start: "100", End: "200"}
	byID, err := applicationTraceSpans(encode([]applicationSpan{s}), id)
	if err != nil || len(byID) != 1 || byID[s.SpanID].ParentID != s.ParentID {
		t.Fatal("lost orphan identity", err)
	}
	if _, err := applicationTraceChain(encode([]applicationSpan{s}), id); err == nil {
		t.Fatal("partial crash trace proved ordinary success")
	}
	for _, bad := range []applicationSpan{
		{TraceID: "fedcba9876543210fedcba9876543210", SpanID: s.SpanID, Start: s.Start, End: s.End},
		{TraceID: id, SpanID: s.SpanID, ParentID: "malformed", Start: s.Start, End: s.End},
		{TraceID: id, SpanID: s.SpanID, Start: "200", End: "100"},
	} {
		if _, err := applicationTraceSpans(encode([]applicationSpan{bad}), id); err == nil {
			t.Fatal("invalid evidence accepted", bad)
		}
	}
	if _, err := applicationTraceSpans(encode([]applicationSpan{s, s}), id); err == nil {
		t.Fatal("duplicate identities accepted")
	}
	if _, err := applicationTraceSpans(encode(nil), id); err == nil {
		t.Fatal("empty trace accepted")
	}
}
