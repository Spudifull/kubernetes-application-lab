package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
)

const publisherRepository = "kubernetes-application-lab-publisher"
const publisherRefFile = ".local-kafka/publisher-image-ref"

var publisherRefPattern = regexp.MustCompile(`^kubernetes-application-lab-publisher:[a-f0-9]{64}$`)

func (l lab) publisherImageRecord(ctx context.Context) error {
	id, err := l.must(ctx, "docker", nil, "image", "inspect", "--format", "{{.Id}}", publisherRepository+":build")
	if err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	ref := publisherRepository + ":" + strings.TrimPrefix(id, "sha256:")
	if !strings.HasPrefix(id, "sha256:") || !publisherRefPattern.MatchString(ref) {
		return errors.New("invalid publisher image ID")
	}
	if _, err := l.must(ctx, "docker", nil, "image", "tag", publisherRepository+":build", ref); err != nil {
		return err
	}
	if err := os.MkdirAll(".local-kafka", 0700); err != nil {
		return err
	}
	if err := os.WriteFile(publisherRefFile, []byte(ref+"\n"), 0600); err != nil {
		return err
	}
	l.print("Recorded publisher image: " + ref)
	return nil
}

func (l lab) publisherAction(ctx context.Context, action string) error {
	if action != "restore-tracing" && action != "deploy-tracing" && action != "deploy-tracing-unavailable" && action != "deploy" && action != "status" && action != "stop" && action != "start" && action != "unreachable" && action != "restore" {
		return errors.New("publisher action must be deploy, status, stop, start, unreachable or restore")
	}
	if err := l.kafkaGuard(ctx); err != nil {
		return err
	}
	switch action {
	case "restore", "restore-tracing":
		// The fault was injected by kubectl-set, which owns this field under
		// server-side apply. Match the release's intent through the same updater
		// before Helm applies it; do not force ownership of unrelated fields.
		values, err := l.must(ctx, "helm", nil, "get", "values", "lab-publisher", "--all", "--output", "json", "--namespace", l.namespace, "--kube-context", l.context)
		if err != nil {
			return err
		}
		var release struct {
			Kafka struct {
				Bootstrap string `json:"bootstrap"`
			} `json:"kafka"`
		}
		if err := json.Unmarshal([]byte(values), &release); err != nil {
			return errors.New("cannot parse publisher release values")
		}
		bootstrap := release.Kafka.Bootstrap
		if bootstrap == "" || !strings.HasSuffix(bootstrap, ".svc.cluster.local:9093") || strings.ContainsAny(bootstrap, " ,\t\r\n") {
			return errors.New("publisher release does not contain the expected internal TLS bootstrap; inspect Helm values before restoring")
		}
		out, err := l.kube(ctx, "set", "env", "deployment/lab-publisher", "-n", l.namespace, "LAB_KAFKA_BOOTSTRAP="+bootstrap)
		if err != nil {
			return err
		}
		l.print(out)
		if action == "restore-tracing" {
			return l.publisherAction(ctx, "deploy-tracing")
		}
		return l.publisherAction(ctx, "deploy")
	case "unreachable":
		// Stop the old producer first: an unready replacement alone would leave
		// the old Ready Pod publishing during a RollingUpdate.
		if err := l.publisherAction(ctx, "stop"); err != nil {
			return err
		}
		out, err := l.kube(ctx, "set", "env", "deployment/lab-publisher", "-n", l.namespace, "LAB_KAFKA_BOOTSTRAP=127.0.0.1:19093")
		if err != nil {
			return err
		}
		l.print(out)
		out, err = l.kube(ctx, "scale", "deployment/lab-publisher", "-n", l.namespace, "--replicas=1")
		if err != nil {
			return err
		}
		l.print(out)
		l.print("Publisher bootstrap deliberately points to its own closed loopback port; Ready=False is expected. Kafka and its PVC are unchanged. Restore with task publisher-unreachable-restore.")
	case "deploy", "deploy-tracing", "deploy-tracing-unavailable":
		b, err := os.ReadFile(publisherRefFile)
		if err != nil {
			return err
		}
		ref := strings.TrimSpace(string(b))
		if !publisherRefPattern.MatchString(ref) {
			return errors.New("invalid publisher image reference")
		}
		tag := strings.TrimPrefix(ref, publisherRepository+":")
		id, err := l.must(ctx, "docker", nil, "image", "inspect", "--format", "{{.Id}}", ref)
		if err != nil {
			return err
		}
		if strings.TrimSpace(id) != "sha256:"+tag {
			return errors.New("publisher image ID does not match tag")
		}
		if _, err := l.kube(ctx, "get", "secret", "lab-publisher-kafka", "-n", l.namespace, "-o", "name"); err != nil {
			return errors.New("run task publisher-secret-create first")
		}
		if _, err := l.must(ctx, "kind", nil, "load", "docker-image", ref, "--name", "kubernetes-application-lab"); err != nil {
			return err
		}
		args := []string{"upgrade", "--install", "lab-publisher", "helm/lab-publisher", "--namespace", l.namespace, "--kube-context", l.context,
			"--reset-values", "--set-string", "image.tag=" + tag, "--wait", "--timeout", "120s"}
		if action == "deploy-tracing" {
			args = append(args, "-f", "cluster/observability/application-tracing-values.yaml")
		}
		if action == "deploy-tracing-unavailable" {
			args = append(args, "-f", "cluster/observability/application-tracing-unavailable-values.yaml")
		}
		out, err := l.must(ctx, "helm", nil, args...)
		if err != nil {
			return err
		}
		l.print(out)
	case "stop", "start":
		replicas := "0"
		if action == "start" {
			replicas = "1"
		}
		out, err := l.kube(ctx, "scale", "deployment/lab-publisher", "-n", l.namespace, "--replicas="+replicas)
		if err != nil {
			return err
		}
		l.print(out)
		if action == "stop" {
			out, err = l.kube(ctx, "wait", "--for=delete", "pod", "-n", l.namespace, "-l", "app.kubernetes.io/name=lab-publisher,app.kubernetes.io/instance=lab-publisher", "--timeout=60s")
		} else {
			out, err = l.kube(ctx, "rollout", "status", "deployment/lab-publisher", "-n", l.namespace, "--timeout=120s")
		}
		if err != nil {
			return err
		}
		l.print(out)
	case "status":
		out, err := l.kube(ctx, "get", "deployment,pod", "-n", l.namespace, "-l", "app.kubernetes.io/name=lab-publisher,app.kubernetes.io/instance=lab-publisher", "-o", "wide")
		if err != nil {
			return err
		}
		l.print(out)
	}
	return nil
}

// Only the three required keys cross namespaces. No secret body is ever passed
// through a normal printing/error helper or stored on disk. Rotation is explicit.
func (l lab) publisherSecretCreate(ctx context.Context) error {
	return l.kafkaIdentitySecretCreate(ctx, "publisher")
}

func (l lab) kafkaIdentitySecretCreate(ctx context.Context, role string) error {
	if role != "publisher" && role != "worker" && role != "dlq-inspector" && role != "keda" {
		return errors.New("unsupported Kafka identity")
	}
	identity := "lab-" + role
	destination := identity + "-kafka"
	label := "Publisher"
	if role == "worker" {
		label = "Worker"
	}
	if role == "dlq-inspector" {
		label = "DLQ inspector"
	}
	if role == "keda" {
		label = "KEDA lag reader"
	}
	if err := l.kafkaGuard(ctx); err != nil {
		return err
	}
	read := func(namespace, name string) (map[string][]byte, error) {
		r, err := l.kubeResult(ctx, "get", "secret", name, "-n", namespace, "--ignore-not-found=true", "-o", "json")
		if err != nil || r.code != 0 {
			return nil, errors.New("cannot read Kafka Secret; details suppressed")
		}
		if strings.TrimSpace(r.out) == "" {
			return nil, nil
		}
		var s struct {
			Data map[string][]byte `json:"data"`
		}
		if json.Unmarshal([]byte(r.out), &s) != nil {
			return nil, errors.New("invalid Kafka Secret JSON")
		}
		return s.Data, nil
	}
	ca, err := read(kafkaNS, "lab-kafka-cluster-ca-cert")
	if err != nil {
		return err
	}
	user, err := read(kafkaNS, identity)
	if err != nil {
		return err
	}
	data := map[string][]byte{"ca.crt": ca["ca.crt"], "user.crt": user["user.crt"], "user.key": user["user.key"]}
	for _, value := range data {
		if len(value) == 0 {
			return errors.New("Kafka CA or client identity missing")
		}
	}
	existing, err := read(l.namespace, destination)
	if err != nil {
		return err
	}
	if existing != nil {
		for key, value := range data {
			if !bytes.Equal(value, existing[key]) {
				return errors.New("Kafka client identity changed; review certificate rotation before replacing existing Secret")
			}
		}
		l.print(label + " Kafka Secret already matches; no credentials changed")
		return nil
	}
	b, err := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]string{"name": destination, "namespace": l.namespace}, "type": "Opaque", "data": data})
	if err != nil {
		return err
	}
	r, err := l.command(ctx, "kubectl", b, "--context", l.context, "create", "-f", "-")
	if err != nil || r.code != 0 {
		return errors.New("Kafka client Secret creation failed; output suppressed to protect credentials")
	}
	l.print("Created " + role + " Kafka Secret in application namespace; no credentials printed or saved")
	return nil
}

func (l lab) outboxStatus(ctx context.Context) error {
	if err := l.kafkaGuard(ctx); err != nil {
		return err
	}
	// Fixed SELECT statements only; never include payloads, hashes or credentials.
	query := `SELECT version FROM public.lab_schema_migrations ORDER BY version;
      SELECT status,count(*) FROM public.lab_jobs GROUP BY status ORDER BY status;
      SELECT count(*) FILTER (WHERE published_at IS NULL) AS pending,
      count(*) FILTER (WHERE published_at IS NOT NULL) AS published,
      coalesce(extract(epoch FROM clock_timestamp()-min(created_at) FILTER (WHERE published_at IS NULL)),0)::bigint AS oldest_pending_seconds FROM public.lab_outbox;
      SELECT j.id,j.idempotency_key,j.status,j.completed_at,
      (SELECT e.result FROM public.lab_job_effects e WHERE e.job_id=j.id) AS result,
      o.event_id,to_jsonb(o)->>'trace_parent' AS trace_parent,o.attempts,o.published_at,o.next_retry_at,o.lease_until,o.error_code,o.kafka_partition,o.kafka_offset,
      (SELECT count(*) FROM public.lab_outbox x WHERE x.job_id=j.id) AS outbox_rows,
      (SELECT count(*) FROM public.lab_job_effects e WHERE e.job_id=j.id) AS effects
      FROM public.lab_jobs j JOIN public.lab_outbox o ON o.job_id=j.id ORDER BY j.created_at DESC LIMIT 30;`
	out, err := l.kube(ctx, "exec", "pod/lab-postgres-0", "-n", l.namespace, "--", "psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", "lab", "-d", "lab", "-c", query)
	if err != nil {
		return err
	}
	l.print(out)
	return nil
}

// These probes use a fixed loopback port-forward, not kubectl proxy: the latter
// cannot attach arbitrary HTTP headers and is not the client data path proof.
func (l lab) jobsProbe(ctx context.Context, baseURL, action, key string, duration time.Duration) error {
	if baseURL != "http://127.0.0.1:18081" {
		return errors.New("jobs probe requires http://127.0.0.1:18081 port-forward")
	}
	if action != "enqueue" && action != "concurrent" && action != "get" {
		return errors.New("jobs action must be enqueue, concurrent or get")
	}
	if err := l.kafkaGuard(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(".local-kafka", 0700); err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil, ResponseHeaderTimeout: 4 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	if action == "get" {
		if key == "" {
			b, err := os.ReadFile(".local-kafka/job-locator.json")
			if err != nil {
				return err
			}
			var loc jobs.Job
			if json.Unmarshal(b, &loc) != nil {
				return errors.New("invalid job locator")
			}
			key = loc.ID
		}
		if !jobs.ValidID(key) {
			return errors.New("KEY must be a job UUID")
		}
		job, status, err := requestJob(ctx, client, baseURL+"/jobs/"+key, nil)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("job get status=%d", status)
		}
		return l.event("job_status", map[string]any{"job": job})
	}
	if key == "" {
		key = fmt.Sprintf("outbox-%d", time.Now().UnixNano())
	}
	workDuration := time.Second
	if duration != 0 {
		workDuration = duration
	}
	if workDuration < time.Millisecond || workDuration > 8*time.Second || workDuration%time.Millisecond != 0 {
		return errors.New("job duration must be whole milliseconds in 1..8000")
	}
	request := jobs.Request{IdempotencyKey: key, JobType: "lab_effect", Payload: jobs.Payload{DurationMS: int(workDuration / time.Millisecond)}}
	if err := request.Validate(); err != nil {
		return err
	}
	count := 1
	if action == "concurrent" {
		count = 8
	}
	type observation struct {
		job     jobs.Job
		traceID string
		status  int
		err     error
	}
	observations := make([]observation, count)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			observations[i].job, observations[i].status, observations[i].err = requestJobObserved(ctx, client, baseURL+"/jobs", &request, func(id string) { observations[i].traceID = id })
		}()
	}
	close(gate)
	wg.Wait()
	var failures []error
	id := observations[0].job.ID
	for i, o := range observations {
		if o.err != nil || o.status != http.StatusAccepted || !jobs.ValidID(o.job.ID) || o.job.ID != id {
			failures = append(failures, fmt.Errorf("request %d status=%d job=%s: %v", i, o.status, o.job.ID, o.err))
		}
		if err := l.event("job_enqueue_response", map[string]any{"request": i, "status": o.status, "job_id": o.job.ID, "idempotency_key": key, "trace_id": o.traceID}); err != nil {
			return err
		}
	}
	if err := errors.Join(failures...); err != nil {
		return err
	}
	if action == "concurrent" {
		if request.Payload.DurationMS == 8000 {
			request.Payload.DurationMS--
		} else {
			request.Payload.DurationMS++
		}
		_, status, err := requestJob(ctx, client, baseURL+"/jobs", &request)
		if err != nil {
			return err
		}
		if err := l.event("job_content_conflict", map[string]any{"status": status, "expected": 409, "job_id": id}); err != nil {
			return err
		}
		if status != http.StatusConflict {
			return fmt.Errorf("different content status=%d, expected409", status)
		}
	}
	b, err := json.Marshal(observations[0].job)
	if err != nil {
		return err
	}
	if err := os.WriteFile(".local-kafka/job-locator.json", b, 0600); err != nil {
		return err
	}
	// Empty content clears a previous locator when tracing is disabled.
	return os.WriteFile(".local-kafka/job-trace-id", []byte(observations[0].traceID), 0600)
}

func requestJob(ctx context.Context, client *http.Client, url string, request *jobs.Request) (jobs.Job, int, error) {
	return requestJobObserved(ctx, client, url, request, nil)
}

func requestJobObserved(ctx context.Context, client *http.Client, url string, request *jobs.Request, observe func(string)) (jobs.Job, int, error) {
	method := http.MethodGet
	var body io.Reader
	if request != nil {
		b, err := json.Marshal(request)
		if err != nil {
			return jobs.Job{}, 0, err
		}
		body = bytes.NewReader(b)
		method = http.MethodPost
	}
	r, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return jobs.Job{}, 0, err
	}
	if request != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(r)
	if err != nil {
		return jobs.Job{}, 0, err
	}
	defer response.Body.Close()
	if observe != nil {
		id := response.Header.Get("X-Lab-Trace-ID")
		if applicationTraceIDPattern.MatchString(id) {
			observe(id)
		}
	}
	b, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	if err != nil {
		return jobs.Job{}, response.StatusCode, err
	}
	if len(b) > 8192 {
		return jobs.Job{}, response.StatusCode, errors.New("job response too large")
	}
	var job jobs.Job
	if response.StatusCode < 300 {
		err = json.Unmarshal(b, &job)
	}
	return job, response.StatusCode, err
}
