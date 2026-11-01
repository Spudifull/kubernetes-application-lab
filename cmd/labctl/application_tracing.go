package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var applicationTraceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type applicationSpan struct {
	TraceID  string `json:"traceId"`
	SpanID   string `json:"spanId"`
	ParentID string `json:"parentSpanId"`
	Name     string `json:"name"`
	Start    string `json:"startTimeUnixNano"`
	End      string `json:"endTimeUnixNano"`
	Service  string `json:"service"`
	Events   []struct {
		Name string `json:"name"`
	} `json:"events,omitempty"`
	Status struct {
		Code json.RawMessage `json:"code,omitempty"`
	} `json:"status"`
}
type applicationBatch struct {
	Resource struct {
		Attributes []struct {
			Key   string
			Value struct{ StringValue string }
		}
	}
	ScopeSpans []struct{ Spans []applicationSpan }
}

func traceIdentifier(value string, size int) string {
	if b, err := hex.DecodeString(value); err == nil && len(b) == size {
		return hex.EncodeToString(b)
	}
	if b, err := base64.StdEncoding.DecodeString(value); err == nil && len(b) == size {
		return hex.EncodeToString(b)
	}
	return ""
}

// Decode stored identities without assuming a complete success path. An abrupt
// process exit can leave exported children whose parent never reached Tempo.
func applicationTraceSpans(data []byte, id string) (map[string]applicationSpan, error) {
	var document struct {
		Batches       []applicationBatch
		ResourceSpans []applicationBatch
	}
	if len(data) > 4<<20 || json.Unmarshal(data, &document) != nil {
		return nil, errors.New("invalid or oversized Tempo document")
	}
	byID := map[string]applicationSpan{}
	for _, b := range append(document.Batches, document.ResourceSpans...) {
		service := ""
		for _, a := range b.Resource.Attributes {
			if a.Key == "service.name" {
				service = a.Value.StringValue
			}
		}
		for _, scope := range b.ScopeSpans {
			for _, s := range scope.Spans {
				if traceIdentifier(s.TraceID, 16) != id {
					return nil, errors.New("unexpected trace ID in Tempo")
				}
				s.SpanID = traceIdentifier(s.SpanID, 8)
				parent := s.ParentID
				s.ParentID = traceIdentifier(parent, 8)
				if parent != "" && s.ParentID == "" {
					return nil, errors.New("invalid parent span ID")
				}
				if s.ParentID == "0000000000000000" {
					s.ParentID = ""
				}
				s.TraceID = id
				s.Service = service
				start, e1 := strconv.ParseInt(s.Start, 10, 64)
				end, e2 := strconv.ParseInt(s.End, 10, 64)
				if s.SpanID == "" || e1 != nil || e2 != nil || start <= 0 || end < start {
					return nil, errors.New("invalid span identity or interval")
				}
				if _, exists := byID[s.SpanID]; exists {
					return nil, errors.New("duplicate span ID in Tempo document")
				}
				byID[s.SpanID] = s
			}
		}
	}
	if len(byID) == 0 {
		return nil, errors.New("Tempo document contains no spans")
	}
	return byID, nil
}

// Verify the ordinary success path from actual stored spans, not log identifiers.
// Async children need not finish inside the already-ended HTTP/producer span.
func applicationTraceChain(data []byte, id string) ([]applicationSpan, error) {
	byID, err := applicationTraceSpans(data, id)
	if err != nil {
		return nil, err
	}
	// Walk from effect to its actual ancestors; unrelated spans with matching
	// names cannot satisfy the proof. Retries may add other sibling attempts.
	wanted := []struct{ service, name string }{
		{"lab-worker", "jobs.complete.transaction"}, {"lab-worker", "lab.jobs.v1 process"},
		{"lab-publisher", "lab.jobs.v1 publish"}, {"lab-publisher", "outbox.publish_attempt"},
		{"lab-api", "jobs.enqueue.transaction"}, {"lab-api", "POST /jobs"},
	}
	for _, candidate := range byID {
		current := candidate
		chain := []applicationSpan{}
		for _, w := range wanted {
			if current.Service != w.service || current.Name != w.name || failedApplicationSpan(current) {
				break
			}
			chain = append(chain, current)
			current = byID[current.ParentID]
		}
		if len(chain) != len(wanted) {
			continue
		}
		// Both SQL completion and offset commit must belong to the same attempt,
		// and commit must start after the SQL transaction has finished.
		for _, commit := range byID {
			if commit.Service != "lab-worker" || commit.Name != "lab-workers commit" || commit.ParentID != chain[1].SpanID || failedApplicationSpan(commit) {
				continue
			}
			end, _ := strconv.ParseInt(chain[0].End, 10, 64)
			start, _ := strconv.ParseInt(commit.Start, 10, 64)
			if start >= end {
				return append(chain, commit), nil
			}
		}
	}
	return nil, errors.New("complete API/outbox/publisher/worker/SQL/offset parent chain not found; save job state and retry after export")
}

func failedApplicationSpan(s applicationSpan) bool {
	code := string(s.Status.Code)
	return code == "2" || code == `"STATUS_CODE_ERROR"`
}

func savedApplicationTraceID(id string) (string, error) {
	if id == "" {
		b, err := os.ReadFile(".local-kafka/job-trace-id")
		if err != nil {
			return "", err
		}
		id = strings.TrimSpace(string(b))
	}
	if !applicationTraceIDPattern.MatchString(id) {
		return "", errors.New("provide a 32-character trace ID from a newly enqueued traced job")
	}
	return id, nil
}

func (e monitoringEvidence) applicationTraceRead(ctx context.Context, id string) error {
	id, err := savedApplicationTraceID(id)
	if err != nil {
		return err
	}
	out, err := e.proxy(ctx, "application-trace", "lab-tempo:3200", "/api/traces/"+id)
	if err != nil {
		return err
	}
	chain, err := applicationTraceChain([]byte(out), id)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(chain, "", "  ")
	if err != nil {
		return err
	}
	if err := e.save("verified-span-chain.json", data); err != nil {
		return err
	}
	return e.lab.event("application_trace_chain", map[string]any{"trace_id": id, "verified_spans": len(chain), "evidence": e.dir, "scope": "stored span identities, parent chain and SQL-before-offset interval; verify final job and effects separately"})
}

func (e monitoringEvidence) applicationTraceInspect(ctx context.Context, id string) error {
	id, err := savedApplicationTraceID(id)
	if err != nil {
		return err
	}
	out, err := e.proxy(ctx, "application-trace", "lab-tempo:3200", "/api/traces/"+id)
	if err != nil {
		return err
	}
	byID, err := applicationTraceSpans([]byte(out), id)
	if err != nil {
		return err
	}
	spans := make([]applicationSpan, 0, len(byID))
	missing := map[string]bool{}
	attempts := 0
	for _, s := range byID {
		spans = append(spans, s)
		if s.Service == "lab-worker" && s.Name == "lab.jobs.v1 process" {
			attempts++
		}
		if s.ParentID != "" {
			if _, ok := byID[s.ParentID]; !ok {
				missing[s.ParentID] = true
			}
		}
	}
	sort.Slice(spans, func(i, j int) bool {
		a, _ := strconv.ParseInt(spans[i].Start, 10, 64)
		b, _ := strconv.ParseInt(spans[j].Start, 10, 64)
		if a == b {
			return spans[i].SpanID < spans[j].SpanID
		}
		return a < b
	})
	missingIDs := make([]string, 0, len(missing))
	for id := range missing {
		missingIDs = append(missingIDs, id)
	}
	sort.Strings(missingIDs)
	b, err := json.MarshalIndent(spans, "", "  ")
	if err != nil {
		return err
	}
	if err := e.save("inspected-spans.json", b); err != nil {
		return err
	}
	return e.lab.event("application_trace_inspected", map[string]any{"trace_id": id, "stored_spans": len(spans), "worker_attempts": attempts, "missing_parent_span_ids": missingIDs, "evidence": e.dir, "scope": "stored identities only; missing spans are not reconstructed; no successful SQL/offset chain assertion"})
}
