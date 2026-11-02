package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMilestone9ObservedEvidence(t *testing.T) {
	root := filepath.Join("..", "..")
	directory := filepath.Join(root, "docs", "verification", "milestone9")
	files, err := filepath.Glob(filepath.Join(directory, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(data) {
			t.Errorf("invalid JSON: %s", path)
		}
	}
	read := func(name string, dst any) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, dst); err != nil {
			t.Fatal(err)
		}
	}
	var drift struct {
		Before    string `json:"git_before"`
		After     string `json:"git_after"`
		Unchanged bool   `json:"git_unchanged"`
		Detected  bool   `json:"observed_out_of_sync"`
		During    int    `json:"live_replicas_during_drift"`
		Restored  int    `json:"restored_replicas"`
		Sync      string `json:"final_sync"`
		Health    string `json:"final_health"`
	}
	read("dev-drift-controlled.json", &drift)
	if drift.Before == "" || drift.Before != drift.After || !drift.Unchanged || !drift.Detected || drift.During != 2 || drift.Restored != 1 || drift.Sync != "Synced" || drift.Health != "Healthy" {
		t.Fatal("controlled drift/self-heal evidence changed")
	}
	var partial struct {
		Detected bool `json:"out_of_sync_observed"`
		Restored bool `json:"desired_replicas_restored"`
	}
	read("dev-drift.json", &partial)
	if partial.Detected || partial.Restored {
		t.Fatal("uncounted initial observation was rewritten as successful")
	}
	var failed releaseObservation
	read("dev-failed-rollout.json", &failed)
	if failed.Health != "Degraded" || failed.Sync != "OutOfSync" || failed.HTTP != 200 {
		t.Fatal("failed rollout/old replica availability evidence changed")
	}
	var recovered releaseObservation
	var registry releaseRegistry
	read("dev-recovered.json", &recovered)
	read("registry.json", &registry)
	data, err := os.ReadFile(filepath.Join(root, "gitops", "candidate.json"))
	if err != nil {
		t.Fatal(err)
	}
	var candidate releaseCandidate
	if err := json.Unmarshal(data, &candidate); err != nil {
		t.Fatal(err)
	}
	dev, err := os.ReadFile(filepath.Join(root, "gitops", "dev", "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	devImage, err := releaseValuesImage(string(dev))
	if err != nil {
		t.Fatal(err)
	}
	stable, err := os.ReadFile(filepath.Join(root, "gitops", "stable", "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	stableImage, err := releaseValuesImage(string(stable))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRelease(candidate, recovered, registry, devImage, stableImage); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseEvidenceRejectsUnsafePromotion(t *testing.T) {
	source := strings.Repeat("a", 40)
	digest := "sha256:" + strings.Repeat("b", 64)
	repo := "ghcr.io/spudifull/kubernetes-application-lab-api"
	baseCandidate := releaseCandidate{Source: source, Repository: repo, Digest: digest, CheckedRun: 1, PublishRun: 2}
	baseRegistry := releaseRegistry{Repository: repo, Digest: digest, Config: "sha256:" + strings.Repeat("c", 64), Anonymous: true, User: "65532:65532", Labels: map[string]string{"org.opencontainers.image.revision": source, "org.opencontainers.image.created": "2026-11-02T00:00:00Z"}}
	var base releaseObservation
	data := `{"environment":"lab-dev","cluster":"kubernetes-application-lab-gitops","gitops_revision":"` + source + `","source_commit_sha":"` + source + `","app_sync":"Synced","app_health":"Healthy","deployment_generation":2,"observed_generation":2,"replicas":1,"ready_replicas":1,"updated_replicas":1,"http_status":200,"version":{"revision":"` + source + `","build_date":"2026-11-02T00:00:00Z"},"pods":[{"containers":[{"ready":true,"image_id":"` + repo + `@` + digest + `"}]}]}`
	if err := json.Unmarshal([]byte(data), &base); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		alter     func(*releaseObservation)
		stable    string
		wantError bool
	}{
		{"verified", func(*releaseObservation) {}, repo + "@" + digest, false},
		{"failed health", func(p *releaseObservation) { p.Health = "Degraded" }, repo + "@" + digest, true},
		{"wrong Pod identity", func(p *releaseObservation) { p.Pods[0].Containers[0].ImageID = "other" }, repo + "@" + digest, true},
		{"stale controller status", func(p *releaseObservation) { p.ObservedGeneration = 1 }, repo + "@" + digest, true},
		{"wrong source version", func(p *releaseObservation) { p.Version.Revision = "other" }, repo + "@" + digest, true},
		{"different stable artifact", func(*releaseObservation) {}, repo + "@sha256:" + strings.Repeat("d", 64), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var proof releaseObservation
			if err := json.Unmarshal([]byte(data), &proof); err != nil {
				t.Fatal(err)
			}
			tc.alter(&proof)
			err := validateRelease(baseCandidate, proof, baseRegistry, repo+"@"+digest, tc.stable)
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestReleaseValuesRejectsDuplicateIdentity(t *testing.T) {
	if _, err := releaseValuesImage("image:\n  repository: ghcr.io/example/api\n  digest: sha256:abc\n  digest: sha256:def\n"); err == nil {
		t.Fatal("duplicate digest accepted")
	}
}
