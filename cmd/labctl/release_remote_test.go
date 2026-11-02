package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func releaseDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func releaseFixture() (releaseCandidate, releaseRegistry) {
	c := releaseCandidate{Source: strings.Repeat("a", 40), Repository: "ghcr.io/spudifull/kubernetes-application-lab-api", Digest: "sha256:" + strings.Repeat("b", 64), CheckedRun: 31, PublishRun: 32}
	r := releaseRegistry{Config: "sha256:" + strings.Repeat("c", 64)}
	return c, r
}
func checkedImageJSON(c releaseCandidate, r releaseRegistry) []byte {
	var image releaseImage
	image.ID = r.Config
	image.Config.User = "65532:65532"
	image.Config.Labels = map[string]string{"org.opencontainers.image.revision": c.Source}
	image.RepoDigests = []string{c.Repository + "@" + c.Digest}
	data, _ := json.Marshal([]releaseImage{image})
	return data
}
func successfulRun(id int64, source, path string) releaseRun {
	r := releaseRun{ID: id, SHA: source, Branch: "dev", Event: "push", Path: path, Status: "completed", Conclusion: "success"}
	r.Repository.Name = releaseRepo
	r.HeadRepository.Name = releaseRepo
	return r
}
func successfulJobs(names ...string) []releaseJob {
	var jobs []releaseJob
	for _, name := range names {
		jobs = append(jobs, releaseJob{Name: name, Status: "completed", Conclusion: "success"})
	}
	return jobs
}

func TestReleaseProvenanceLayoutsAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		unified bool
		alter   func(*releaseCandidate, *releaseRun, *releaseRun, *[]releaseJob)
		bad     bool
	}{
		{name: "historical separate runs"},
		{name: "unified checked and published run", unified: true},
		{name: "wrong SHA", alter: func(_ *releaseCandidate, c, p *releaseRun, _ *[]releaseJob) { c.SHA = strings.Repeat("d", 40) }, bad: true},
		{name: "pull request run", alter: func(_ *releaseCandidate, c, p *releaseRun, _ *[]releaseJob) { c.Event = "pull_request" }, bad: true},
		{name: "fork repository", alter: func(_ *releaseCandidate, c, p *releaseRun, _ *[]releaseJob) { c.HeadRepository.Name = "other/repo" }, bad: true},
		{name: "failed run", alter: func(_ *releaseCandidate, c, p *releaseRun, _ *[]releaseJob) { c.Conclusion = "failure" }, bad: true},
		{name: "untrusted workflow", alter: func(_ *releaseCandidate, c, p *releaseRun, _ *[]releaseJob) { c.Path = ".github/workflows/other.yml" }, bad: true},
		{name: "skipped kind", alter: func(_ *releaseCandidate, c, p *releaseRun, j *[]releaseJob) { (*j)[1].Conclusion = "skipped" }, bad: true},
		{name: "missing kind", alter: func(_ *releaseCandidate, c, p *releaseRun, j *[]releaseJob) { *j = (*j)[:1] }, bad: true},
		{name: "duplicate kind", alter: func(_ *releaseCandidate, c, p *releaseRun, j *[]releaseJob) { *j = append(*j, (*j)[1]) }, bad: true},
		{name: "unpublished unified run", unified: true, alter: func(_ *releaseCandidate, c, p *releaseRun, j *[]releaseJob) { (*j)[5].Conclusion = "skipped" }, bad: true},
		{name: "different unified publish run", unified: true, alter: func(c *releaseCandidate, _ *releaseRun, p *releaseRun, _ *[]releaseJob) { c.PublishRun = 33; p.ID = 33 }, bad: true},
		{name: "failed Go in unified run", unified: true, alter: func(_ *releaseCandidate, c, p *releaseRun, j *[]releaseJob) { (*j)[2].Conclusion = "failure" }, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := releaseFixture()
			checked := successfulRun(c.CheckedRun, c.Source, ".github/workflows/container.yml")
			published := successfulRun(c.PublishRun, c.Source, ".github/workflows/publish.yml")
			jobs := successfulJobs("container", "kind")
			if tc.unified {
				c.PublishRun = c.CheckedRun
				checked.Path = ".github/workflows/ci.yml"
				published = checked
				jobs = successfulJobs("container", "kind", "go", "evidence", "helm", "publish / publish")
			}
			if tc.alter != nil {
				tc.alter(&c, &checked, &published, &jobs)
			}
			pages := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("GitHub bearer token missing")
				}
				endpoint := strings.TrimPrefix(r.URL.Path, "/repos/"+releaseRepo+"/")
				switch endpoint {
				case fmt.Sprintf("actions/runs/%d", c.CheckedRun):
					_ = json.NewEncoder(w).Encode(checked)
				case fmt.Sprintf("actions/runs/%d", c.PublishRun):
					_ = json.NewEncoder(w).Encode(published)
				case fmt.Sprintf("actions/runs/%d/jobs", c.CheckedRun):
					pages++
					part := jobs
					if len(jobs) > 1 {
						if r.URL.Query().Get("page") == "1" {
							part = jobs[:1]
						} else {
							part = jobs[1:]
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"total_count": len(jobs), "jobs": part})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			remote := releaseRemote{client: server.Client(), api: server.URL, token: "test-token"}
			err := remote.provenance(t.Context(), c)
			if (err != nil) != tc.bad {
				t.Fatalf("error=%v", err)
			}
			if !tc.bad && pages != 2 {
				t.Fatalf("jobs pagination was not exercised: %d", pages)
			}
		})
	}
}

func TestReleaseEventRejectsUntrustedBase(t *testing.T) {
	var p releasePR
	p.Base.SHA = strings.Repeat("a", 40)
	p.Base.Ref = "main"
	p.Base.Repo.Name = releaseRepo
	p.Head.Ref = "dev"
	p.Head.Repo.Name = releaseRepo
	for _, tc := range []struct {
		name  string
		alter func(*releasePR)
		bad   bool
	}{
		{"trusted", func(*releasePR) {}, false},
		{"fork", func(p *releasePR) { p.Head.Repo.Name = "fork/repo" }, true},
		{"feature head", func(p *releasePR) { p.Head.Ref = "feature/test" }, true},
		{"other base", func(p *releasePR) { p.Base.Ref = "dev" }, true},
		{"ref instead of immutable SHA", func(p *releasePR) { p.Base.SHA = "main" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := p
			tc.alter(&value)
			data, _ := json.Marshal(map[string]any{"pull_request": value})
			_, err := trustedReleaseBase(data, releaseRepo, "pull_request")
			if (err != nil) != tc.bad {
				t.Fatalf("error=%v", err)
			}
		})
	}
	data, _ := json.Marshal(map[string]any{"pull_request": p})
	if _, err := trustedReleaseBase(data, releaseRepo, "push"); err == nil {
		t.Fatal("push event accepted as release PR")
	}
}

func TestAcceptedReleaseIdentityRequiresWholeReceipt(t *testing.T) {
	c, _ := releaseFixture()
	receipt := []byte(`{"config_digest":"checked","unknown":{"verified":true}}`)
	for _, tc := range []struct {
		name      string
		candidate releaseCandidate
		data      []byte
		reuse     bool
	}{
		{"accepted", c, receipt, true},
		{"changed config", c, []byte(`{"config_digest":"other","unknown":{"verified":true}}`), false},
		{"changed unknown field", c, []byte(`{"config_digest":"checked","unknown":{"verified":false}}`), false},
		{"changed run", func() releaseCandidate { v := c; v.CheckedRun++; return v }(), receipt, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := acceptedReleaseIdentity(tc.candidate, c, tc.data, receipt)
			if err != nil || got != tc.reuse {
				t.Fatalf("reuse=%v error=%v", got, err)
			}
		})
	}
	reuse, err := acceptedReleaseIdentity(c, c, []byte(`{"counter":9007199254740992}`), []byte(`{"counter":9007199254740993}`))
	if err != nil || reuse {
		t.Fatal("large JSON numbers lost precision in trusted receipt comparison")
	}
}

func imageZIP(t *testing.T, names []string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	for _, name := range names {
		w, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestNewCandidateRequiresVerifiedOriginalArtifact(t *testing.T) {
	for _, tc := range []struct {
		name                                     string
		expired, badDigest, badConfig, duplicate bool
	}{
		{name: "checked artifact"}, {name: "expired artifact", expired: true}, {name: "tampered archive", badDigest: true}, {name: "wrong config", badConfig: true}, {name: "duplicate metadata", duplicate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, registry := releaseFixture()
			image := checkedImageJSON(c, registry)
			names := []string{"image.json"}
			if tc.duplicate {
				names = append(names, "image.json")
			}
			data := imageZIP(t, names, image)
			artifact := releaseArtifact{ID: 5, Name: "container-evidence", Digest: releaseDigest(data), Expired: tc.expired}
			if tc.badDigest {
				artifact.Digest = "sha256:" + strings.Repeat("0", 64)
			}
			if tc.badConfig {
				registry.Config = "sha256:" + strings.Repeat("d", 64)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/artifacts") {
					_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 1, "artifacts": []releaseArtifact{artifact}})
					return
				}
				if strings.HasSuffix(r.URL.Path, "/zip") {
					_, _ = w.Write(data)
					return
				}
				http.NotFound(w, r)
			}))
			defer server.Close()
			remote := releaseRemote{client: server.Client(), api: server.URL, token: "test-token"}
			err := remote.imageEvidence(t.Context(), c, registry)
			bad := tc.expired || tc.badDigest || tc.badConfig || tc.duplicate
			if (err != nil) != bad {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if _, err := releaseImageFromZIP(imageZIP(t, []string{"../image.json"}, []byte(`[]`))); err == nil {
		t.Fatal("path outside root image.json accepted")
	}
}

func TestAnonymousRegistryVerifiesContentAndConfig(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		badConfig, badDigest bool
	}{
		{name: "verified"}, {name: "wrong config", badConfig: true}, {name: "tampered manifest", badDigest: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, registry := releaseFixture()
			manifest, _ := json.Marshal(map[string]any{"config": map[string]string{"digest": registry.Config}})
			c.Digest = releaseDigest(manifest)
			if tc.badDigest {
				manifest = append(manifest, ' ')
			}
			if tc.badConfig {
				registry.Config = "other"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					if r.Header.Get("Authorization") != "" {
						t.Error("credential sent to anonymous token endpoint")
					}
					if r.URL.Query().Get("scope") != "repository:spudifull/kubernetes-application-lab-api:pull" {
						t.Error("wrong registry scope")
					}
					_, _ = io.WriteString(w, `{"token":"anonymous-pull"}`)
					return
				}
				if r.Header.Get("Authorization") != "Bearer anonymous-pull" {
					t.Error("GitHub token leaked to registry or anonymous token missing")
				}
				_, _ = w.Write(manifest)
			}))
			defer server.Close()
			remote := releaseRemote{client: server.Client(), registry: server.URL, token: "github-token-must-not-leak"}
			err := remote.registryIdentity(t.Context(), c, registry)
			if (err != nil) != (tc.badConfig || tc.badDigest) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

type releaseTransportFunc func(*http.Request) (*http.Response, error)

func (f releaseTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestReleaseHTTPBoundsCancellationAndRedaction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "oversized response") }))
	defer server.Close()
	remote := releaseRemote{client: server.Client()}
	if _, err := remote.get(t.Context(), server.URL, "", "", 4); err == nil {
		t.Fatal("oversized response accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := remote.get(ctx, server.URL, "", "", 100); !errors.Is(err, context.Canceled) {
		t.Fatalf("context cancellation lost: %v", err)
	}
	remote.client = &http.Client{Transport: releaseTransportFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("secret-token signed-download-url")
	})}
	_, err := remote.get(t.Context(), "https://example.invalid", "", "", 100)
	if err == nil || strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "signed-download") {
		t.Fatalf("sensitive transport error leaked: %v", err)
	}
	prod := newReleaseRemote("token")
	req, _ := http.NewRequest(http.MethodGet, "https://download.example.invalid", nil)
	req.Header.Set("Authorization", "Bearer token")
	if err := prod.client.CheckRedirect(req, nil); err != nil || req.Header.Get("Authorization") != "" {
		t.Fatal("authorization forwarded to artifact redirect")
	}
	req.URL.Scheme = "http"
	if err := prod.client.CheckRedirect(req, nil); err == nil {
		t.Fatal("insecure redirect accepted")
	}
}

func TestPublishChecksSameRunAndCreatesCanonicalProposal(t *testing.T) {
	t.Chdir(t.TempDir())
	c, r := releaseFixture()
	for key, value := range map[string]string{"SOURCE_SHA": c.Source, "CHECKED_RUN_ID": "31", "GITHUB_SHA": c.Source, "GITHUB_RUN_ID": "31", "GITHUB_REPOSITORY": releaseRepo, "GITHUB_REF": "refs/heads/dev", "GITHUB_EVENT_NAME": "push", "GITHUB_WORKFLOW_REF": releaseRepo + "/.github/workflows/ci.yml@refs/heads/dev"} {
		t.Setenv(key, value)
	}
	if err := os.MkdirAll(".cache/promotion", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".cache/promotion/image.json", checkedImageJSON(c, r), 0600); err != nil {
		t.Fatal(err)
	}
	l := lab{out: io.Discard}
	if err := l.publishCheck(false); err != nil {
		t.Fatal(err)
	}
	if err := l.publishCheck(true); err != nil {
		t.Fatal(err)
	}
	var proposal releaseCandidate
	if err := readReleaseJSON(filepath.Join(".cache", "promotion", "candidate.json"), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.CheckedRun != 31 || proposal.PublishRun != 31 || proposal.Digest != c.Digest || proposal.Source != c.Source {
		t.Fatalf("wrong proposal: %+v", proposal)
	}
	for key, value := range map[string]string{"CHECKED_RUN_ID": "other", "SOURCE_SHA": strings.Repeat("f", 40), "GITHUB_EVENT_NAME": "pull_request", "GITHUB_REPOSITORY": "fork/repo", "GITHUB_REF": "refs/heads/main", "GITHUB_WORKFLOW_REF": releaseRepo + "/.github/workflows/other.yml@refs/heads/dev"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, value)
			if err := l.publishCheck(false); err == nil {
				t.Fatal("untrusted publication accepted")
			}
		})
	}
}
