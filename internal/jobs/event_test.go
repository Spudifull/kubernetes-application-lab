package jobs

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validEvent() Event {
	return Event{SchemaVersion: 1, EventID: "00000000-0000-4000-8000-000000000001", JobID: "00000000-0000-4000-8000-000000000002", JobType: "lab_effect", CreatedAt: time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC), Payload: Payload{DurationMS: 1000}}
}
func TestDecodeEvent(t *testing.T) {
	e := validEvent()
	body, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, key, body string
		valid           bool
	}{
		{"valid", e.JobID, string(body), true}, {"key", e.EventID, string(body), false},
		{"unknown field", e.JobID, strings.TrimSuffix(string(body), "}") + `,"other":1}`, false},
		{"trailing", e.JobID, string(body) + ` {}`, false},
		{"version", e.JobID, strings.Replace(string(body), `"schema_version":1`, `"schema_version":2`, 1), false},
		{"duration", e.JobID, strings.Replace(string(body), `"duration_ms":1000`, `"duration_ms":8001`, 1), false},
		{"type", e.JobID, strings.Replace(string(body), "lab_effect", "other", 1), false},
		{"oversize", e.JobID, strings.Repeat("x", 4097), false}, {"bad JSON", e.JobID, `{`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeEvent([]byte(tt.key), []byte(tt.body))
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v err=%v", tt.valid, err)
			}
			if err == nil && got != e {
				t.Fatal("event changed")
			}
		})
	}
}
func FuzzDecodeEvent(f *testing.F) {
	e := validEvent()
	b, _ := json.Marshal(e)
	f.Add(e.JobID, b)
	f.Add("", []byte(`{}`))
	f.Fuzz(func(t *testing.T, key string, body []byte) {
		e, err := DecodeEvent([]byte(key), body)
		if err != nil {
			return
		}
		encoded, err := json.Marshal(e)
		if err != nil || len(encoded) > 4096 {
			t.Fatal("accepted event exceeds bound")
		}
		again, err := DecodeEvent([]byte(key), encoded)
		if err != nil || !again.CreatedAt.Equal(e.CreatedAt) {
			t.Fatal("canonical roundtrip changed event time")
		}
		// Equivalent UTC spellings may decode with different Location pointers.
		e.CreatedAt = again.CreatedAt
		if again != e {
			t.Fatal("canonical roundtrip changed event")
		}
	})
}
