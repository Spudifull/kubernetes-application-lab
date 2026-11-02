package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
)

type releaseCandidate struct {
	Source              string `json:"source_commit_sha"`
	Repository          string `json:"image_repository"`
	Digest              string `json:"image_digest"`
	CheckedRun          int64  `json:"checked_container_run_id"`
	PublishRun          int64  `json:"publish_run_id"`
	Promotion           string `json:"promotion_commit_sha"`
	DevObservation      string `json:"dev_observation"`
	RegistryObservation string `json:"registry_observation"`
}
type releaseObservation struct {
	Environment        string `json:"environment"`
	NamespaceUID       string `json:"namespace_uid"`
	DeploymentUID      string `json:"deployment_uid"`
	ServiceUID         string `json:"service_uid"`
	ApplicationUID     string `json:"application_uid"`
	Branch             string `json:"tracked_branch"`
	Release            string `json:"helm_release"`
	Promotion          string `json:"promotion_commit_sha"`
	Cluster            string `json:"cluster"`
	GitRevision        string `json:"gitops_revision"`
	Source             string `json:"source_commit_sha"`
	Sync               string `json:"app_sync"`
	Health             string `json:"app_health"`
	Generation         int    `json:"deployment_generation"`
	ObservedGeneration int    `json:"observed_generation"`
	Replicas           int    `json:"replicas"`
	Ready              int    `json:"ready_replicas"`
	Updated            int    `json:"updated_replicas"`
	HTTP               int    `json:"http_status"`
	Version            struct {
		Revision string `json:"revision"`
		Date     string `json:"build_date"`
	} `json:"version"`
	Pods []struct {
		Deletion   *string `json:"deletion_timestamp"`
		Containers []struct {
			Ready   bool   `json:"ready"`
			ImageID string `json:"image_id"`
		} `json:"containers"`
	} `json:"pods"`
}
type releaseRegistry struct {
	Repository string            `json:"repository"`
	Digest     string            `json:"registry_manifest_digest"`
	Config     string            `json:"config_digest"`
	Anonymous  bool              `json:"anonymous_pull"`
	User       string            `json:"runtime_user"`
	Labels     map[string]string `json:"oci_labels"`
}

func validateRelease(c releaseCandidate, p releaseObservation, r releaseRegistry, devImage, stableImage string) error {
	sha := regexp.MustCompile(`^[a-f0-9]{40}$`)
	digest := regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	if !sha.MatchString(c.Source) || !digest.MatchString(c.Digest) || c.Repository != "ghcr.io/spudifull/kubernetes-application-lab-api" || c.CheckedRun <= 0 || c.PublishRun <= 0 {
		return errors.New("invalid release candidate identity or provenance")
	}
	expected := c.Repository + "@" + c.Digest
	if devImage != expected || stableImage != expected {
		return errors.New("development and stable must use the same candidate digest")
	}
	if r.Repository != c.Repository || r.Digest != c.Digest || !digest.MatchString(r.Config) || !r.Anonymous || r.User != "65532:65532" || r.Labels["org.opencontainers.image.revision"] != c.Source {
		return errors.New("registry receipt does not match the candidate")
	}
	if p.Environment != "lab-dev" || p.Cluster != "kubernetes-application-lab-gitops" || !sha.MatchString(p.GitRevision) || p.Source != c.Source || p.Sync != "Synced" || p.Health != "Healthy" {
		return errors.New("candidate has no successful development GitOps observation")
	}
	if p.Generation <= 0 || p.Generation != p.ObservedGeneration || p.Replicas <= 0 || p.Ready != p.Replicas || p.Updated != p.Replicas || p.HTTP != 200 || p.Version.Revision != c.Source || p.Version.Date == "" || p.Version.Date != r.Labels["org.opencontainers.image.created"] {
		return errors.New("development readiness, version or build date is not verified")
	}
	ready := 0
	for _, pod := range p.Pods {
		if pod.Deletion != nil {
			continue
		}
		for _, container := range pod.Containers {
			if !container.Ready || container.ImageID != expected {
				return errors.New("observed Pod imageID differs from the promoted digest or is not ready")
			}
			ready++
		}
	}
	if ready < p.Replicas {
		return errors.New("missing ready Pod image identities")
	}
	return nil
}

func releaseValuesImage(data string) (string, error) {
	field := func(name string) (string, error) {
		matches := regexp.MustCompile(`(?m)^[ \t]+`+name+`:[ \t]*([^\r\n#]*)`).FindAllStringSubmatch(data, -1)
		if len(matches) != 1 {
			return "", fmt.Errorf("values must contain exactly one image.%s", name)
		}
		return strings.Trim(strings.TrimSpace(matches[0][1]), `"'`), nil
	}
	repository, err := field("repository")
	if err != nil {
		return "", err
	}
	digest, err := field("digest")
	if err != nil {
		return "", err
	}
	return repository + "@" + digest, nil
}

func readReleaseJSON(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func releaseObservationPath(selected, fallback string) (string, error) {
	if selected == "" {
		selected = fallback
	}
	if path.Clean(selected) != selected || !strings.HasPrefix(selected, "docs/verification/milestone9/") || strings.ContainsAny(selected, `\:`) || !strings.HasSuffix(selected, ".json") {
		return "", errors.New("observation path must stay inside milestone9 evidence")
	}
	return selected, nil
}

func (l lab) releaseCheck(ctx context.Context) error {
	var candidate releaseCandidate
	var proof releaseObservation
	var registry releaseRegistry
	if err := readReleaseJSON("gitops/candidate.json", &candidate); err != nil {
		return err
	}
	devPath, err := releaseObservationPath(candidate.DevObservation, "docs/verification/milestone9/dev-recovered.json")
	if err != nil {
		return err
	}
	registryPath, err := releaseObservationPath(candidate.RegistryObservation, "docs/verification/milestone9/registry.json")
	if err != nil {
		return err
	}
	if err := readReleaseJSON(devPath, &proof); err != nil {
		return err
	}
	if err := readReleaseJSON(registryPath, &registry); err != nil {
		return err
	}
	images := make([]string, 0, 2)
	for _, path := range []string{"gitops/dev/values.yaml", "gitops/stable/values.yaml"} {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		image, err := releaseValuesImage(string(data))
		if err != nil {
			return err
		}
		images = append(images, image)
	}
	if err := validateRelease(candidate, proof, registry, images[0], images[1]); err != nil {
		return err
	}
	// Only API build inputs are compared. Lab tooling and environment values are not linked into the API.
	if _, err := l.must(ctx, "git", nil, "diff", "--exit-code", candidate.Source, "HEAD", "--", "cmd/api", "internal", "go.mod", "go.sum", "docker/api.Dockerfile", ".dockerignore"); err != nil {
		return fmt.Errorf("API build inputs changed after the checked artifact: %w", err)
	}
	historic, err := l.must(ctx, "git", nil, "show", proof.GitRevision+":gitops/dev/values.yaml")
	if err != nil {
		return err
	}
	image, err := releaseValuesImage(historic)
	if err != nil {
		return err
	}
	if image != images[0] {
		return errors.New("observed GitOps revision used a different image")
	}
	l.print("Archived development proof and unchanged API inputs match the promotion candidate; this is not a live cluster check.")
	return nil
}
