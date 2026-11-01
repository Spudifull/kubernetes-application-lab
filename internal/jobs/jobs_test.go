package jobs

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRequestAndCanonicalHash(t *testing.T) {
	good := Request{"one", "lab_effect", Payload{1000}}
	for _, test := range []struct {
		name  string
		r     Request
		valid bool
	}{
		{"valid", good, true}, {"empty key", Request{"", "lab_effect", Payload{1000}}, false},
		{"key characters", Request{"a b", "lab_effect", Payload{1000}}, false},
		{"type", Request{"one", "unknown", Payload{1000}}, false},
		{"zero", Request{"one", "lab_effect", Payload{0}}, false},
		{"upper bound", Request{"one", "lab_effect", Payload{8000}}, true},
		{"too long", Request{"one", "lab_effect", Payload{8001}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if (test.r.Validate() == nil) != test.valid {
				t.Fatalf("validation=%v", test.r.Validate())
			}
		})
	}
	var reordered Request
	if err := json.Unmarshal([]byte(`{"payload":{"duration_ms":1000},"job_type":"lab_effect","idempotency_key":"two"}`), &reordered); err != nil {
		t.Fatal(err)
	}
	if good.Hash() != reordered.Hash() {
		t.Fatal("key or field order changed content hash")
	}
	reordered.Payload.DurationMS++
	if good.Hash() == reordered.Hash() {
		t.Fatal("different content shares hash")
	}
}

func TestIDsAndRetryBound(t *testing.T) {
	ids := map[string]bool{}
	for range 100 {
		id, err := newID()
		if err != nil || !ValidID(id) || ids[id] {
			t.Fatalf("id=%s err=%v", id, err)
		}
		ids[id] = true
	}
	for i, d := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second} {
		if got := RetryDelay(int64(i + 1)); got != d {
			t.Fatalf("attempt%d=%s", i+1, got)
		}
	}
	if RetryDelay(1<<62) != 30*time.Second {
		t.Fatal("retry overflow or missing cap")
	}
}

func FuzzRequestHash(f *testing.F) {
	f.Add("key", 1000)
	f.Add("", -1)
	f.Fuzz(func(t *testing.T, key string, duration int) {
		r := Request{key, "lab_effect", Payload{duration}}
		if r.Validate() != nil {
			return
		}
		b, err := json.Marshal(r)
		if err != nil || len(b) > 2048 {
			t.Fatalf("valid request exceeds body bound: %v", err)
		}
		var decoded Request
		if err := json.Unmarshal(b, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Hash() != r.Hash() || decoded.Validate() != nil {
			t.Fatal("roundtrip changes validated content")
		}
	})
}
