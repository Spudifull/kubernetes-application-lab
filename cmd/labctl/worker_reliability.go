package main

import (
	"context"
	"errors"
)

func workerReliabilityAction(action string) bool {
	switch action {
	case "retry-success", "retry-exhaust", "dlq-read", "poison":
		return true
	}
	return false
}

func (l lab) requireWorkerStopped(ctx context.Context) error {
	var deployment struct{ Spec struct{ Replicas int } }
	if err := l.getJSON(ctx, &deployment, "get", "deployment/lab-worker", "-n", l.namespace); err != nil {
		return err
	}
	var pods podList
	if err := l.getJSON(ctx, &pods, "get", "pods", "-n", l.namespace, "-l", "app.kubernetes.io/name=lab-worker,app.kubernetes.io/instance=lab-worker"); err != nil {
		return err
	}
	if deployment.Spec.Replicas != 0 || len(pods.Items) != 0 {
		return errors.New("run task worker-stop and wait for all worker Pods to disappear before configuring a fault")
	}
	return nil
}

func (l lab) workerReliabilityStatus(ctx context.Context) error {
	if err := l.kafkaGuard(ctx); err != nil {
		return err
	}
	query := `SELECT r.job_id,j.status,r.failures_before_success,r.failure_count,r.next_retry_at,j.completed_at,j.error_code,
        (SELECT count(*) FROM public.lab_job_effects e WHERE e.job_id=j.id) AS effects
        FROM public.lab_worker_retries r JOIN public.lab_jobs j ON j.id=r.job_id ORDER BY j.created_at DESC LIMIT 30;
        SELECT source_topic,source_partition,source_offset,job_id,envelope->>'reason' AS reason,
        published_at,dlq_partition,dlq_offset FROM public.lab_dead_letters ORDER BY created_at DESC LIMIT 30;`
	out, err := l.kube(ctx, "exec", "pod/lab-postgres-0", "-n", l.namespace, "--", "psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", "lab", "-d", "lab", "-c", query)
	if err != nil {
		return err
	}
	l.print(out)
	return nil
}
