// Package scheduled derives the outbox request for one logical CronJob run.
package scheduled

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
)

const TimestampAnnotation = "batch.kubernetes.io/cronjob-scheduled-timestamp"

var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Request uses the scheduled instant, never Pod start time or a Pod/Job UID.
// Namespace and CronJob name form the identity; recreating the same named
// CronJob deliberately preserves that identity while the SQL key exists.
func Request(namespace, cronName, timestamp string) (jobs.Request, error) {
	if !namePattern.MatchString(namespace) || !namePattern.MatchString(cronName) {
		return jobs.Request{}, errors.New("invalid scheduled namespace or CronJob name")
	}
	at, err := time.Parse(time.RFC3339, timestamp)
	if err != nil || at.IsZero() || at.Nanosecond() != 0 {
		return jobs.Request{}, errors.New("scheduled timestamp must be RFC3339 with whole seconds")
	}
	digest := sha256.Sum256([]byte(namespace + "/" + cronName + "/" + at.UTC().Format(time.RFC3339)))
	// 5 + 56 characters fits the existing 64-character idempotency contract.
	return jobs.Request{IdempotencyKey: fmt.Sprintf("cron_%x", digest[:28]), JobType: "lab_effect", Payload: jobs.Payload{DurationMS: 1000}}, nil
}

type jobMetadata struct {
	Metadata struct {
		Name, Namespace string
		Annotations     map[string]string
		OwnerReferences []struct {
			APIVersion string `json:"apiVersion"`
			Kind, Name string
			Controller bool
		}
	}
}

// ScheduledTimestamp reads only the parent Job. Kubernetes places the original
// scheduled timestamp on the Job, not on its Pod. Missing metadata fails closed.
func ScheduledTimestamp(ctx context.Context, client *http.Client, baseURL, token, namespace, jobName, cronName string) (string, error) {
	if !namePattern.MatchString(namespace) || !namePattern.MatchString(jobName) || !namePattern.MatchString(cronName) {
		return "", errors.New("invalid scheduled Job identity")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/apis/batch/v1/namespaces/"+url.PathEscape(namespace)+"/jobs/"+url.PathEscape(jobName), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("cannot read parent Job from Kubernetes API")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("read parent Job: Kubernetes HTTP %d", resp.StatusCode)
	}
	var job jobMetadata
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&job); err != nil {
		return "", errors.New("invalid parent Job response")
	}
	if job.Metadata.Name != jobName || job.Metadata.Namespace != namespace {
		return "", errors.New("parent Job identity mismatch")
	}
	owned := false
	for _, owner := range job.Metadata.OwnerReferences {
		if owner.APIVersion == "batch/v1" && owner.Kind == "CronJob" && owner.Name == cronName && owner.Controller {
			owned = true
		}
	}
	if !owned {
		return "", errors.New("parent Job is not controlled by the expected CronJob")
	}
	timestamp := job.Metadata.Annotations[TimestampAnnotation]
	if _, err := Request(namespace, cronName, timestamp); err != nil {
		return "", err
	}
	return timestamp, nil
}
