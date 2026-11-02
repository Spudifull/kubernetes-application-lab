package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestHistoricalEvidence(t *testing.T) {
	root := filepath.Join("..", "..", "docs", "verification")
	index, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Join(root, "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 22 {
		t.Fatalf("expected 22 evidence files, got %d", len(files))
	}
	seen := map[string]bool{}
	for _, f := range files {
		t.Run(f.Name(), func(t *testing.T) {
			name := strings.ToLower(f.Name())
			if seen[name] {
				t.Fatal("duplicate public filename")
			}
			seen[name] = true
			data, err := os.ReadFile(filepath.Join(root, "evidence", f.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Ext(name) == ".json" && !json.Valid(data) {
				t.Fatal("invalid JSON")
			}
			row := regexp.MustCompile(`(?m)^\| \[` + regexp.QuoteMeta(f.Name()) + `\].*$`).Find(index)
			if !strings.Contains(string(row), fmt.Sprintf("%x", sha256.Sum256(data))) {
				t.Fatal("missing index entry or SHA-256 mismatch")
			}
			if regexp.MustCompile(`(?i)([a-z]:\\+Users\\+|/home/|/Users/|-----BEGIN .*PRIVATE KEY|Bearer\s+[A-Za-z0-9._-]+)`).Match(data) {
				t.Fatal("forbidden private path or credential pattern")
			}
		})
	}
	var counter struct {
		Samples []struct{ Value string }
		SQL     struct {
			Status  string
			Effects int
		}
		Offsets []struct {
			Lag             int
			CommittedOffset int `json:"committed_offset"`
			LogEndOffset    int `json:"log_end_offset"`
		}
	}
	b, err := os.ReadFile(filepath.Join(root, "evidence", "worker-crash-counter-2026-11-01.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &counter); err != nil {
		t.Fatal(err)
	}
	if len(counter.Samples) != 2 || counter.Samples[0].Value != "2" || counter.Samples[1].Value != "3" || counter.SQL.Status != "succeeded" || counter.SQL.Effects != 1 {
		t.Fatal("redelivery/counter invariant violated")
	}
	if len(counter.Offsets) != 3 {
		t.Fatal("missing partition observations")
	}
	for _, p := range counter.Offsets {
		if p.Lag != 0 || p.CommittedOffset != p.LogEndOffset {
			t.Fatal("consumer did not recover")
		}
	}
	b, err = os.ReadFile(filepath.Join(root, "evidence", "api-unavailable-partial-2026-11-01.json"))
	if err != nil {
		t.Fatal(err)
	}
	var partial struct{ Status string }
	if err := json.Unmarshal(b, &partial); err != nil {
		t.Fatal(err)
	}
	if partial.Status != "incomplete cycle: observer timed out before API restore; no Resolved evidence yet" {
		t.Fatal("partial failure was rewritten")
	}
}

func TestMarkdownLocalLinks(t *testing.T) {
	root := filepath.Join("..", "..")
	link := regexp.MustCompile(`\]\(([^\s)]+)\)`)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || strings.HasPrefix(d.Name(), ".local") || d.Name() == ".publication-audit" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range link.FindAllSubmatch(b, -1) {
			target := strings.SplitN(string(m[1]), "#", 2)[0]
			if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), filepath.FromSlash(target))); err != nil {
				t.Errorf("%s: missing link %s", path, target)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHistoricalAlertFingerprint(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "verification", "evidence", "alerting-outbox-cycle-2026-11-01.json"))
	if err != nil {
		t.Fatal(err)
	}
	var archive struct {
		Proof struct {
			Pending, Firing bool
			Fingerprint     string
		}
		Polls []struct {
			RuleState string `json:"rule_state"`
			Receiver  struct {
				Notifications []struct{ Status, Fingerprint string }
			}
		}
	}
	if err := json.Unmarshal(data, &archive); err != nil {
		t.Fatal(err)
	}
	if !archive.Proof.Pending || !archive.Proof.Firing || archive.Proof.Fingerprint == "" || len(archive.Polls) == 0 {
		t.Fatal("missing alert cycle")
	}
	states := map[string]bool{}
	for _, poll := range archive.Polls {
		states[poll.RuleState] = true
	}
	if !states["pending"] || !states["firing"] {
		t.Fatal("missing observed Pending/Firing")
	}
	last := archive.Polls[len(archive.Polls)-1]
	if last.RuleState != "inactive" || len(last.Receiver.Notifications) != 2 {
		t.Fatal("missing recovery notifications")
	}
	for i, notification := range last.Receiver.Notifications {
		if notification.Fingerprint != archive.Proof.Fingerprint || notification.Status != []string{"firing", "resolved"}[i] {
			t.Fatal("alert fingerprint or notification order changed")
		}
	}
}
