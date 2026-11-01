package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
)

const workerRepository = "kubernetes-application-lab-worker"
const workerRefFile = ".local-kafka/worker-image-ref"

var workerRefPattern = regexp.MustCompile(`^kubernetes-application-lab-worker:[a-f0-9]{64}$`)

func (l lab) workerImageRecord(ctx context.Context) error {
	id, err := l.must(ctx, "docker", nil, "image", "inspect", "--format", "{{.Id}}", workerRepository+":build")
	if err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	ref := workerRepository + ":" + strings.TrimPrefix(id, "sha256:")
	if !strings.HasPrefix(id, "sha256:") || !workerRefPattern.MatchString(ref) {
		return errors.New("invalid worker image ID")
	}
	if _, err := l.must(ctx, "docker", nil, "image", "tag", workerRepository+":build", ref); err != nil {
		return err
	}
	if err := os.MkdirAll(".local-kafka", 0700); err != nil {
		return err
	}
	if err := os.WriteFile(workerRefFile, []byte(ref+"\n"), 0600); err != nil {
		return err
	}
	l.print("Recorded worker image: " + ref)
	return nil
}
func (l lab) workerImage(ctx context.Context) (string, error) {
	b, err := os.ReadFile(workerRefFile)
	if err != nil {
		return "", err
	}
	ref := strings.TrimSpace(string(b))
	if !workerRefPattern.MatchString(ref) {
		return "", errors.New("invalid worker image reference")
	}
	id, err := l.must(ctx, "docker", nil, "image", "inspect", "--format", "{{.Id}}", ref)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(id) != "sha256:"+strings.TrimPrefix(ref, workerRepository+":") {
		return "", errors.New("worker image ID does not match tag")
	}
	return ref, nil
}
func (l lab) workerAction(ctx context.Context, action string) error {
	if action != "deploy-tracing" && action != "deploy-tracing-unavailable" && action != "deploy" && action != "deploy-two" && action != "deploy-crash" && action != "deploy-crash-tracing" && action != "deploy-dlq-unreachable" && action != "status" && action != "stop" {
		return errors.New("worker action must be deploy, deploy-tracing, deploy-tracing-unavailable, deploy-two, deploy-crash, deploy-crash-tracing, deploy-dlq-unreachable, status or stop")
	}
	if err := l.kafkaGuard(ctx); err != nil {
		return err
	}
	if action == "status" {
		out, err := l.kube(ctx, "get", "deployment,pod", "-n", l.namespace, "-l", "app.kubernetes.io/name=lab-worker,app.kubernetes.io/instance=lab-worker", "-o", "wide")
		if err != nil {
			return err
		}
		l.print(out)
		return nil
	}
	if err := l.requireFixedWorker(ctx); err != nil {
		return err
	}
	crashJobID := ""
	if action == "deploy-crash" || action == "deploy-crash-tracing" {
		// A running old replica could commit the target before the fault is armed.
		if err := l.requireWorkerStopped(ctx); err != nil {
			return err
		}
		b, err := os.ReadFile(".local-kafka/job-locator.json")
		if err != nil {
			return err
		}
		var job jobs.Job
		if json.Unmarshal(b, &job) != nil || !jobs.ValidID(job.ID) {
			return errors.New("crash experiment requires a valid saved job locator from task job-enqueue")
		}
		crashJobID = job.ID
		l.print("Arming crash before offset commit for job " + crashJobID)
	}
	ref, err := l.workerImage(ctx)
	if err != nil {
		return err
	}
	if _, err := l.kube(ctx, "get", "secret", "lab-worker-kafka", "-n", l.namespace, "-o", "name"); err != nil {
		return errors.New("run task worker-secret-create first")
	}
	if action != "stop" {
		if _, err := l.must(ctx, "kind", nil, "load", "docker-image", ref, "--name", "kubernetes-application-lab"); err != nil {
			return err
		}
	}
	args := l.workerHelmArgs(action, ref, crashJobID)
	// The fault intentionally prevents a healthy rollout. Its result is verified
	// from previous-container logs, SQL and group offsets, not Helm readiness.
	out, err := l.must(ctx, "helm", nil, args...)
	if err != nil {
		return err
	}
	l.print(out)
	if action == "stop" {
		// Explicit names make a Pod that disappeared between list and wait a
		// successful deletion condition, not a no-resources selector error.
		var pods podList
		if err := l.getJSON(ctx, &pods, "get", "pods", "-n", l.namespace, "-l", "app.kubernetes.io/name=lab-worker,app.kubernetes.io/instance=lab-worker"); err != nil {
			return err
		}
		for _, p := range pods.Items {
			out, err := l.kube(ctx, "wait", "--for=delete", "pod/"+p.Metadata.Name, "-n", l.namespace, "--timeout=45s")
			if err != nil {
				return err
			}
			l.print(out)
		}
	}
	return nil
}

func (l lab) workerHelmArgs(action, ref, crashJobID string) []string {
	replicas := "1"
	if action == "deploy-two" {
		replicas = "2"
	}
	if action == "stop" {
		replicas = "0"
	}
	args := []string{"upgrade", "--install", "lab-worker", "helm/lab-worker", "--namespace", l.namespace, "--kube-context", l.context,
		"--reset-values", "--set-string", "image.tag=" + strings.TrimPrefix(ref, workerRepository+":"), "--set", "replicaCount=" + replicas,
		"--set-string", "lab.crashBeforeOffsetJobID=" + crashJobID, "--timeout", "120s"}
	if action == "deploy-tracing" || action == "deploy-crash-tracing" {
		args = append(args, "-f", "cluster/observability/application-tracing-values.yaml")
	}
	if action == "deploy-tracing-unavailable" {
		args = append(args, "-f", "cluster/observability/application-tracing-unavailable-values.yaml")
	}
	dlqBootstrap := ""
	if action == "deploy-dlq-unreachable" {
		dlqBootstrap = "127.0.0.1:19094"
	}
	args = append(args, "--set-string", "lab.dlqBootstrap="+dlqBootstrap)
	if action != "deploy-crash" && action != "deploy-crash-tracing" {
		args = append(args, "--wait")
	}
	return args
}

// Fixed replica and fault experiments must not race a scaler controlling the
// same Deployment. Discovery handles clusters where KEDA is not installed.
func (l lab) requireFixedWorker(ctx context.Context) error {
	r, err := l.kubeResult(ctx, "get", "crd", "scaledobjects.keda.sh", "--ignore-not-found=true", "-o", "name")
	if err != nil || r.code != 0 {
		return errors.New("cannot inspect KEDA ownership before worker mutation")
	}
	if strings.TrimSpace(r.out) == "" {
		return nil
	}
	var list struct {
		Items []struct {
			Spec struct{ ScaleTargetRef struct{ Name string } }
		}
	}
	if err := l.getJSON(ctx, &list, "get", "scaledobjects", "-n", l.namespace); err != nil {
		return err
	}
	for _, so := range list.Items {
		if so.Spec.ScaleTargetRef.Name == "lab-worker" {
			return errors.New("KEDA controls lab-worker; run task keda-disable before fixed replica or fault commands")
		}
	}
	return nil
}

func workerDiagnosticPod(name, image, action, id string) map[string]any {
	args := []string{"group-status"}
	secret := "lab-worker-kafka"
	if action == "replay" {
		args = []string{"replay", id}
		secret = "lab-publisher-kafka"
	}
	switch action {
	case "retry-success":
		args = []string{"configure-retry", id, "2"}
	case "retry-exhaust":
		args = []string{"configure-retry", id, "3"}
	case "poison":
		args, secret = []string{"poison", id}, "lab-publisher-kafka"
	case "dlq-read":
		args, secret = []string{"dlq-read", id}, "lab-dlq-inspector-kafka"
	}
	pod := kafkaSmokePod(name, image, args)
	pod["metadata"] = map[string]any{"name": name, "namespace": mainNS, "labels": map[string]string{"app.kubernetes.io/name": "lab-worker-diagnostic"}}
	spec := pod["spec"].(map[string]any)
	container := spec["containers"].([]any)[0].(map[string]any)
	env := []any{
		map[string]any{"name": "LAB_KAFKA_BOOTSTRAP", "value": "lab-kafka-kafka-bootstrap.kubernetes-application-lab-kafka.svc.cluster.local:9093"},
		map[string]any{"name": "LAB_KAFKA_CA_FILE", "value": "/kafka/ca.crt"}, map[string]any{"name": "LAB_KAFKA_CERT_FILE", "value": "/kafka/user.crt"}, map[string]any{"name": "LAB_KAFKA_KEY_FILE", "value": "/kafka/user.key"},
	}
	if action == "replay" || action == "retry-success" || action == "retry-exhaust" || action == "dlq-read" {
		env = append(env, dbEnv()...)
	}
	container["env"] = env
	container["volumeMounts"] = []any{map[string]any{"name": "kafka", "mountPath": "/kafka", "readOnly": true}}
	spec["volumes"] = []any{map[string]any{"name": "kafka", "secret": map[string]any{"secretName": secret}}}
	if action == "retry-success" || action == "retry-exhaust" {
		// Fault setup only touches SQL; it does not need a Kafka identity.
		container["env"] = dbEnv()
		delete(container, "volumeMounts")
		delete(spec, "volumes")
	}
	return pod
}
func (l lab) workerDiagnostic(ctx context.Context, action, id string) error {
	if action != "group-status" && action != "replay" && action != "cleanup" && !workerReliabilityAction(action) {
		return errors.New("unsupported worker diagnostic action")
	}
	if err := l.kafkaGuard(ctx); err != nil {
		return err
	}
	if action == "cleanup" {
		out, err := l.kube(ctx, "delete", "pods", "-n", l.namespace, "-l", "app.kubernetes.io/name=lab-worker-diagnostic", "--ignore-not-found=true", "--wait=true")
		if err != nil {
			return err
		}
		l.print(out)
		return nil
	}
	if action == "retry-success" || action == "retry-exhaust" {
		if err := l.requireWorkerStopped(ctx); err != nil {
			return err
		}
	}
	usesJob := action == "replay" || action == "retry-success" || action == "retry-exhaust" || action == "dlq-read"
	if usesJob && id == "" {
		b, err := os.ReadFile(".local-kafka/job-locator.json")
		if err != nil {
			return err
		}
		var job jobs.Job
		if json.Unmarshal(b, &job) != nil {
			return errors.New("invalid saved job locator")
		}
		id = job.ID
	}
	if usesJob && !jobs.ValidID(id) && !(action == "dlq-read" && id == "latest-invalid") {
		return errors.New("diagnostic requires a job UUID (DLQ read also accepts latest-invalid)")
	}
	if action == "poison" && id != "invalid-json" && id != "unknown-version" && id != "unknown-type" && id != "missing-job" && id != "oversized" {
		return errors.New("unsupported poison mode")
	}
	ref, err := l.workerImage(ctx)
	if err != nil {
		return err
	}
	if _, err := l.must(ctx, "kind", nil, "load", "docker-image", ref, "--name", "kubernetes-application-lab"); err != nil {
		return err
	}
	name := fmt.Sprintf("lab-worker-diagnostic-%d", time.Now().UnixNano())
	body, err := json.Marshal(workerDiagnosticPod(name, ref, action, id))
	if err != nil {
		return err
	}
	if _, err := l.kubeInput(ctx, body, "create", "-f", "-"); err != nil {
		return err
	}
	l.print("Diagnostic Pod: " + name)
	_, waitErr := l.kube(ctx, "wait", "--for=jsonpath={.status.phase}=Succeeded", "pod/"+name, "-n", l.namespace, "--timeout=90s")
	logs, logErr := l.kube(ctx, "logs", "pod/"+name, "-n", l.namespace)
	if logErr == nil {
		l.print(logs)
	}
	l.print("Diagnostic Pod retained; cleanup explicitly after saving logs.")
	return errors.Join(waitErr, logErr)
}

func (l lab) workerBatch(ctx context.Context, url string) error {
	return l.workerBatchSized(ctx, url, 12, 1000, "worker", 90*time.Second)
}

func (l lab) workerBatchSized(ctx context.Context, url string, count, durationMS int, prefix string, timeout time.Duration) error {
	if url != "http://127.0.0.1:18081" {
		return errors.New("worker batch requires the local API port-forward")
	}
	if err := l.kafkaGuard(ctx); err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil, ResponseHeaderTimeout: 4 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	var batch []jobs.Job
	run := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	for i := range count {
		request := jobs.Request{IdempotencyKey: fmt.Sprintf("%s-%d", run, i), JobType: "lab_effect", Payload: jobs.Payload{DurationMS: durationMS}}
		job, status, err := requestJob(ctx, client, url+"/jobs", &request)
		if err != nil {
			return err
		}
		if status != 202 || !jobs.ValidID(job.ID) {
			return fmt.Errorf("batch enqueue status=%d", status)
		}
		batch = append(batch, job)
		if err := l.event("worker_batch_enqueued", map[string]any{"run_id": run, "job_id": job.ID, "index": i}); err != nil {
			return err
		}
	}
	body, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(".local-kafka", 0700); err != nil {
		return err
	}
	if err := os.WriteFile(".local-kafka/worker-batch.json", body, 0600); err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for i := range batch {
		for {
			job, status, err := requestJob(waitCtx, client, url+"/jobs/"+batch[i].ID, nil)
			if err != nil {
				return err
			}
			if status != 200 {
				return fmt.Errorf("batch get status=%d", status)
			}
			if job.Status == "succeeded" {
				var effect jobs.Effect
				if json.Unmarshal(job.Result, &effect) != nil || effect.Outcome != "completed" || effect.DurationMS != durationMS {
					return errors.New("batch result differs from expected effect")
				}
				if err := l.event("worker_batch_succeeded", map[string]any{"job": job, "run_id": run}); err != nil {
					return err
				}
				break
			}
			if job.Status == "failed" {
				return errors.New("batch job failed")
			}
			select {
			case <-waitCtx.Done():
				return waitCtx.Err()
			case <-ticker.C:
			}
		}
	}
	return l.event("worker_batch_completed", map[string]any{"run_id": run, "jobs": len(batch)})
}
