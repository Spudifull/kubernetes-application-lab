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
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const releaseRepo = "Spudifull/kubernetes-application-lab"

type releaseRun struct {
	ID         int64  `json:"id"`
	SHA        string `json:"head_sha"`
	Branch     string `json:"head_branch"`
	Event      string `json:"event"`
	Path       string `json:"path"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Repository struct {
		Name string `json:"full_name"`
	} `json:"repository"`
	HeadRepository struct {
		Name string `json:"full_name"`
	} `json:"head_repository"`
}
type releaseJob struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}
type releaseArtifact struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Digest  string `json:"digest"`
	Expired bool   `json:"expired"`
}
type releasePR struct {
	Base struct {
		SHA  string `json:"sha"`
		Ref  string `json:"ref"`
		Repo struct {
			Name string `json:"full_name"`
		} `json:"repo"`
	} `json:"base"`
	Head struct {
		Ref  string `json:"ref"`
		Repo struct {
			Name string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
}

func trustedReleaseBase(event []byte, repository, eventName string) (string, error) {
	var payload struct {
		PR releasePR `json:"pull_request"`
	}
	if err := json.Unmarshal(event, &payload); err != nil {
		return "", errors.New("invalid GitHub pull request event")
	}
	p := payload.PR
	if repository != releaseRepo || eventName != "pull_request" || p.Base.Ref != "main" || p.Base.Repo.Name != releaseRepo || p.Head.Ref != "dev" || p.Head.Repo.Name != releaseRepo || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(p.Base.SHA) {
		return "", errors.New("release requires a same-repository dev to main PR with an exact trusted base SHA")
	}
	return p.Base.SHA, nil
}

// Clients and API roots are injected by tests; production roots are fixed below.
// Error messages never include response bodies, bearer tokens or signed URLs.
type releaseRemote struct {
	client   *http.Client
	api      string
	registry string
	token    string
}

func newReleaseRemote(token string) releaseRemote {
	return releaseRemote{client: &http.Client{
		Timeout: 45 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 || req.URL.Scheme != "https" {
				return errors.New("unsafe release download redirect")
			}
			req.Header.Del("Authorization")
			return nil
		},
	}, api: "https://api.github.com", registry: "https://ghcr.io", token: token}
}

func (r releaseRemote) get(ctx context.Context, target, auth, accept string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, errors.New("invalid release endpoint")
	}
	req.Header.Set("Accept", accept)
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := r.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("release HTTP request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release HTTP status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errors.New("read release HTTP response failed")
	}
	if int64(len(data)) > limit {
		return nil, errors.New("release HTTP response exceeds size limit")
	}
	return data, nil
}

func (r releaseRemote) github(ctx context.Context, endpoint string, dst any) error {
	if r.token == "" {
		return errors.New("GH_TOKEN is required for provenance checks")
	}
	data, err := r.get(ctx, r.api+"/repos/"+releaseRepo+"/"+endpoint, r.token, "application/vnd.github+json", 4<<20)
	if err != nil {
		return fmt.Errorf("GitHub provenance: %w", err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return errors.New("invalid GitHub provenance JSON")
	}
	return nil
}

func validateReleaseRun(run releaseRun, id int64, source string, paths ...string) error {
	allowed := false
	for _, path := range paths {
		allowed = allowed || run.Path == path
	}
	if !allowed || run.ID != id || run.SHA != source || run.Branch != "dev" || run.Event != "push" || run.Status != "completed" || run.Conclusion != "success" || run.Repository.Name != releaseRepo || run.HeadRepository.Name != releaseRepo {
		return errors.New("run does not prove successful trusted push/dev provenance for this source")
	}
	return nil
}

func (r releaseRemote) jobs(ctx context.Context, id int64) ([]releaseJob, error) {
	var jobs []releaseJob
	for page := 1; page <= 10; page++ {
		var result struct {
			Total int          `json:"total_count"`
			Jobs  []releaseJob `json:"jobs"`
		}
		if err := r.github(ctx, fmt.Sprintf("actions/runs/%d/jobs?per_page=100&page=%d", id, page), &result); err != nil {
			return nil, err
		}
		jobs = append(jobs, result.Jobs...)
		if len(jobs) >= result.Total {
			return jobs, nil
		}
		if len(result.Jobs) == 0 {
			break
		}
	}
	return nil, errors.New("incomplete workflow jobs pagination")
}

func requireReleaseJobs(jobs []releaseJob, names ...string) error {
	for _, name := range names {
		count := 0
		for _, job := range jobs {
			if job.Name == name {
				count++
				if job.Status != "completed" || job.Conclusion != "success" {
					return fmt.Errorf("required job %s did not succeed", name)
				}
			}
		}
		if count != 1 {
			return fmt.Errorf("expected one required job %s, found %d", name, count)
		}
	}
	return nil
}

func (r releaseRemote) provenance(ctx context.Context, c releaseCandidate) error {
	var checked, published releaseRun
	if err := r.github(ctx, fmt.Sprintf("actions/runs/%d", c.CheckedRun), &checked); err != nil {
		return err
	}
	if err := validateReleaseRun(checked, c.CheckedRun, c.Source, ".github/workflows/container.yml", ".github/workflows/ci.yml"); err != nil {
		return fmt.Errorf("checked image: %w", err)
	}
	jobs, err := r.jobs(ctx, c.CheckedRun)
	if err != nil {
		return err
	}
	required := []string{"container", "kind"}
	if checked.Path == ".github/workflows/ci.yml" {
		required = append(required, "go", "evidence", "helm")
	}
	if err := requireReleaseJobs(jobs, required...); err != nil {
		return err
	}
	if err := r.github(ctx, fmt.Sprintf("actions/runs/%d", c.PublishRun), &published); err != nil {
		return err
	}
	if err := validateReleaseRun(published, c.PublishRun, c.Source, ".github/workflows/publish.yml", ".github/workflows/ci.yml"); err != nil {
		return fmt.Errorf("publication: %w", err)
	}
	if published.Path == ".github/workflows/ci.yml" {
		if c.PublishRun != c.CheckedRun || checked.Path != published.Path {
			return errors.New("publication must belong to the checked CI run")
		}
		if err := requireReleaseJobs(jobs, "publish / publish"); err != nil {
			return err
		}
	} else {
		publishedJobs, err := r.jobs(ctx, c.PublishRun)
		if err != nil {
			return err
		}
		if err := requireReleaseJobs(publishedJobs, "publish"); err != nil {
			return err
		}
	}
	return nil
}

// Compare the complete JSON receipt, including fields unknown to the typed validator.
func acceptedReleaseIdentity(c, accepted releaseCandidate, receipt, baseline []byte) (bool, error) {
	decode := func(data []byte) (any, error) {
		if !json.Valid(data) {
			return nil, errors.New("invalid registry receipt JSON")
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var value any
		err := decoder.Decode(&value)
		return value, err
	}
	current, err := decode(receipt)
	if err != nil {
		return false, err
	}
	previous, err := decode(baseline)
	if err != nil {
		return false, err
	}
	same := c.Source == accepted.Source && c.Repository == accepted.Repository && c.Digest == accepted.Digest && c.CheckedRun == accepted.CheckedRun && c.PublishRun == accepted.PublishRun
	return same && reflect.DeepEqual(current, previous), nil
}

type releaseImage struct {
	ID          string   `json:"Id"`
	RepoDigests []string `json:"RepoDigests"`
	Config      struct {
		User   string            `json:"User"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

func validateReleaseImage(data []byte, source, config string) (releaseImage, error) {
	var images []releaseImage
	if err := json.Unmarshal(data, &images); err != nil || len(images) != 1 {
		return releaseImage{}, errors.New("expected exactly one checked image")
	}
	image := images[0]
	if !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(image.ID) || config != "" && image.ID != config || image.Config.User != "65532:65532" || image.Config.Labels["org.opencontainers.image.revision"] != source {
		return releaseImage{}, errors.New("checked image config, source revision or non-root identity mismatch")
	}
	return image, nil
}

func (r releaseRemote) imageEvidence(ctx context.Context, c releaseCandidate, registry releaseRegistry) error {
	var matches []releaseArtifact
	count := 0
	for page := 1; page <= 10; page++ {
		var result struct {
			Total     int               `json:"total_count"`
			Artifacts []releaseArtifact `json:"artifacts"`
		}
		if err := r.github(ctx, fmt.Sprintf("actions/runs/%d/artifacts?per_page=100&page=%d", c.CheckedRun, page), &result); err != nil {
			return err
		}
		count += len(result.Artifacts)
		for _, artifact := range result.Artifacts {
			if artifact.Name == "container-evidence" {
				matches = append(matches, artifact)
			}
		}
		if count >= result.Total {
			break
		}
		if len(result.Artifacts) == 0 || page == 10 {
			return errors.New("incomplete artifact pagination")
		}
	}
	if len(matches) != 1 || matches[0].Expired || matches[0].ID <= 0 {
		return errors.New("new candidate requires one unexpired original container-evidence artifact")
	}
	artifact := matches[0]
	data, err := r.get(ctx, fmt.Sprintf("%s/repos/%s/actions/artifacts/%d/zip", r.api, releaseRepo, artifact.ID), r.token, "application/vnd.github+json", 32<<20)
	if err != nil {
		return fmt.Errorf("checked image artifact: %w", err)
	}
	sum := sha256.Sum256(data)
	if artifact.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return errors.New("checked artifact archive digest mismatch")
	}
	image, err := releaseImageFromZIP(data)
	if err != nil {
		return err
	}
	_, err = validateReleaseImage(image, c.Source, registry.Config)
	return err
}

func releaseImageFromZIP(data []byte) ([]byte, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("invalid checked artifact ZIP")
	}
	var image []byte
	count := 0
	for _, file := range archive.File {
		if file.Name != "image.json" {
			continue
		}
		count++
		if count > 1 || file.UncompressedSize64 > 4<<20 {
			return nil, errors.New("duplicate or oversized image metadata")
		}
		reader, err := file.Open()
		if err != nil {
			return nil, errors.New("cannot open checked image metadata")
		}
		image, err = io.ReadAll(io.LimitReader(reader, (4<<20)+1))
		closeErr := reader.Close()
		if err != nil || closeErr != nil || len(image) > 4<<20 {
			return nil, errors.New("cannot read bounded checked image metadata")
		}
	}
	if count != 1 {
		return nil, errors.New("missing image.json in checked artifact")
	}
	return image, nil
}

func (r releaseRemote) registryIdentity(ctx context.Context, c releaseCandidate, receipt releaseRegistry) error {
	repository := strings.TrimPrefix(c.Repository, "ghcr.io/")
	query := url.Values{"service": {"ghcr.io"}, "scope": {"repository:" + repository + ":pull"}}
	data, err := r.get(ctx, r.registry+"/token?"+query.Encode(), "", "application/json", 1<<20)
	if err != nil {
		return fmt.Errorf("anonymous registry access: %w", err)
	}
	var token struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &token); err != nil || token.Token == "" {
		return errors.New("invalid anonymous registry token response")
	}
	data, err = r.get(ctx, r.registry+"/v2/"+repository+"/manifests/"+c.Digest, token.Token, "application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.manifest.v1+json", 4<<20)
	if err != nil {
		return fmt.Errorf("registry manifest: %w", err)
	}
	sum := sha256.Sum256(data)
	if c.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return errors.New("registry manifest content digest mismatch")
	}
	var manifest struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Config.Digest != receipt.Config {
		return errors.New("current registry config does not match the accepted receipt")
	}
	return nil
}

func (l lab) releaseVerify(ctx context.Context) error {
	event, err := os.ReadFile(os.Getenv("GITHUB_EVENT_PATH"))
	if err != nil {
		return errors.New("GitHub PR event is required")
	}
	base, err := trustedReleaseBase(event, os.Getenv("GITHUB_REPOSITORY"), os.Getenv("GITHUB_EVENT_NAME"))
	if err != nil {
		return err
	}
	if err := l.releaseCheck(ctx); err != nil {
		return err
	}
	var candidate, accepted releaseCandidate
	if err := readReleaseJSON("gitops/candidate.json", &candidate); err != nil {
		return err
	}
	registryPath, err := releaseObservationPath(candidate.RegistryObservation, "docs/verification/milestone9/registry.json")
	if err != nil {
		return err
	}
	receipt, err := os.ReadFile(registryPath)
	if err != nil {
		return err
	}
	var registry releaseRegistry
	if err := json.Unmarshal(receipt, &registry); err != nil {
		return err
	}
	baseline, err := l.must(ctx, "git", nil, "show", base+":gitops/candidate.json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(baseline), &accepted); err != nil {
		return err
	}
	previousPath, err := releaseObservationPath(accepted.RegistryObservation, "docs/verification/milestone9/registry.json")
	if err != nil {
		return err
	}
	previous, err := l.must(ctx, "git", nil, "show", base+":"+previousPath)
	if err != nil {
		return err
	}
	reuse, err := acceptedReleaseIdentity(candidate, accepted, receipt, []byte(previous))
	if err != nil {
		return err
	}
	remote := newReleaseRemote(os.Getenv("GH_TOKEN"))
	if err := remote.provenance(ctx, candidate); err != nil {
		return err
	}
	if reuse {
		l.print("Reusing image identity accepted by protected main at " + base + "; no expired artifact download required.")
	} else if err := remote.imageEvidence(ctx, candidate, registry); err != nil {
		return err
	}
	if err := remote.registryIdentity(ctx, candidate, registry); err != nil {
		return err
	}
	l.print("Trusted CI/publication provenance and current anonymous registry identity verified.")
	return nil
}

func publicationContext() (string, int64, error) {
	source := os.Getenv("SOURCE_SHA")
	run := os.Getenv("CHECKED_RUN_ID")
	id, err := strconv.ParseInt(run, 10, 64)
	if err != nil || id <= 0 || run != os.Getenv("GITHUB_RUN_ID") || source != os.Getenv("GITHUB_SHA") || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(source) || os.Getenv("GITHUB_REPOSITORY") != releaseRepo || os.Getenv("GITHUB_REF") != "refs/heads/dev" || os.Getenv("GITHUB_EVENT_NAME") != "push" || os.Getenv("GITHUB_WORKFLOW_REF") != releaseRepo+"/.github/workflows/ci.yml@refs/heads/dev" {
		return "", 0, errors.New("publication requires the same trusted push/dev source and checked run")
	}
	return source, id, nil
}

func (l lab) publishCheck(receipt bool) error {
	source, id, err := publicationContext()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(".cache/promotion/image.json")
	if err != nil {
		return err
	}
	image, err := validateReleaseImage(data, source, "")
	if err != nil {
		return err
	}
	if !receipt {
		l.print("Checked API image source and non-root identity verified.")
		return nil
	}
	repository := "ghcr.io/spudifull/kubernetes-application-lab-api"
	digest := ""
	for _, ref := range image.RepoDigests {
		if strings.HasPrefix(ref, repository+"@") {
			value := strings.TrimPrefix(ref, repository+"@")
			if !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(value) || digest != "" {
				return errors.New("ambiguous published image digest")
			}
			digest = value
		}
	}
	if digest == "" {
		return errors.New("missing published registry digest")
	}
	proposal := struct {
		Source     string  `json:"source_commit_sha"`
		Repository string  `json:"image_repository"`
		Digest     string  `json:"image_digest"`
		Config     string  `json:"config_digest"`
		Checked    int64   `json:"checked_container_run_id"`
		Published  int64   `json:"publish_run_id"`
		Promotion  *string `json:"promotion_commit_sha"`
	}{source, repository, digest, image.ID, id, id, nil}
	data, err = json.MarshalIndent(proposal, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(".cache/promotion/candidate.json", append(data, '\n'), 0600)
}
