package main

import (
	"testing"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
)

func TestInvalidFixturesStayBounded(t *testing.T) {
	for _, mode := range []string{"invalid-json", "unknown-version", "unknown-type", "missing-job", "oversized"} {
		r, err := invalidRecord(mode)
		if err != nil || r.Topic != jobs.Topic || len(r.Value) > 4097 || len(r.Key) != 36 {
			t.Fatalf("mode=%s record=%v err=%v", mode, r, err)
		}
		_, err = jobs.DecodeEvent(r.Key, r.Value)
		if (err == nil) != (mode == "missing-job") {
			t.Fatalf("unexpected fixture validity: mode=%s err=%v", mode, err)
		}
	}
	if _, err := invalidRecord("arbitrary"); err == nil {
		t.Fatal("unbounded mode accepted")
	}
}
