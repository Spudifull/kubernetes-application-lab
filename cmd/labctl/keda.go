package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
)

const kedaChartVersion = "2.21.0"

func (l lab) kedaAction(ctx context.Context, action string) error {
	switch action {
	case "preflight", "render", "install", "identity", "secret", "status", "enable", "disable", "load", "check", "observe":
	default:
		return errors.New("unsupported KEDA action")
	}
	if err := l.kafkaGuard(ctx); err != nil {
		return err
	}
	switch action {
	case "preflight":
		if err := l.kafkaKubectlVersion(ctx); err != nil {
			return err
		}
		for _, args := range [][]string{{"get", "nodes", "-o", "wide"}, {"describe", "nodes"}, {"get", "deployment,hpa,cronjob", "-n", l.namespace, "-o", "wide"}, {"get", "apiservice", "v1beta1.external.metrics.k8s.io", "--ignore-not-found=true", "-o", "yaml"}} {
			out, err := l.kube(ctx, args...)
			if err != nil {
				return err
			}
			l.print(out)
		}
		return nil
	case "render", "install":
		// External Metrics API registration is cluster-wide. Refuse to replace
		// another provider or another KEDA release with this lab installation.
		var api struct {
			Metadata struct{ Annotations map[string]string }
		}
		r, err := l.kubeResult(ctx, "get", "apiservice", "v1beta1.external.metrics.k8s.io", "--ignore-not-found=true", "-o", "json")
		if err != nil || r.code != 0 {
			return errors.New("cannot inspect External Metrics API provider")
		}
		if strings.TrimSpace(r.out) != "" {
			if json.Unmarshal([]byte(r.out), &api) != nil || api.Metadata.Annotations["meta.helm.sh/release-name"] != "lab-keda" || api.Metadata.Annotations["meta.helm.sh/release-namespace"] != mainNS {
				return errors.New("another External Metrics provider is present; inspect it instead of replacing it")
			}
		}
		args := []string{"template", "lab-keda", "keda", "--include-crds"}
		if action == "install" {
			if err := l.kafkaKubectlVersion(ctx); err != nil {
				return err
			}
			args = []string{"upgrade", "--install", "lab-keda", "keda", "--wait", "--timeout", "300s"}
		}
		args = append(args, "--repo", "https://kedacore.github.io/charts", "--version", kedaChartVersion, "--namespace", l.namespace, "--kube-context", l.context, "-f", "cluster/keda/operator-values.yaml")
		out, err := l.must(ctx, "helm", nil, args...)
		if err != nil {
			return err
		}
		if action == "render" {
			if err := os.MkdirAll(".local-kafka", 0700); err != nil {
				return err
			}
			if err := os.WriteFile(".local-kafka/keda-rendered.yaml", []byte(out), 0600); err != nil {
				return err
			}
			l.print("Review .local-kafka/keda-rendered.yaml before installing KEDA; no cluster resources changed.")
			return nil
		}
		l.print(out)
		return nil
	case "identity":
		for _, dry := range []bool{true, false} {
			args := []string{"apply", "-f", "cluster/keda/user.yaml"}
			if dry {
				args = append(args, "--dry-run=server")
			}
			out, err := l.kube(ctx, args...)
			if err != nil {
				return err
			}
			l.print(out)
		}
		out, err := l.kube(ctx, "wait", "--for=condition=Ready", "kafkauser/lab-keda", "-n", kafkaNS, "--timeout=120s")
		l.print(out)
		return err
	case "secret":
		return l.kafkaIdentitySecretCreate(ctx, "keda")
	case "status":
		for _, args := range [][]string{
			{"get", "deployment,pod", "-n", l.namespace, "-l", "app.kubernetes.io/instance=lab-keda", "-o", "wide"},
			{"get", "scaledobject,triggerauthentication,hpa", "-n", l.namespace, "-o", "yaml"},
			{"get", "deployment,pod", "-n", l.namespace, "-l", "app.kubernetes.io/instance=lab-worker", "-o", "wide"},
			{"get", "apiservice", "v1beta1.external.metrics.k8s.io", "-o", "wide"},
		} {
			out, err := l.kube(ctx, args...)
			if err != nil {
				return err
			}
			l.print(out)
		}
		return nil
	case "enable", "disable":
		return l.kedaWorker(ctx, action == "enable")
	case "load":
		var so struct {
			Metadata struct{ Annotations map[string]string }
			Status   struct {
				Conditions []struct{ Type, Status string }
			}
		}
		if err := l.getJSON(ctx, &so, "get", "scaledobject/lab-worker", "-n", l.namespace); err != nil {
			return err
		}
		ready := false
		for _, c := range so.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				ready = true
			}
		}
		if !ready || so.Metadata.Annotations["autoscaling.keda.sh/paused-replicas"] != "" {
			return errors.New("KEDA must be Ready and unpaused before creating load")
		}
		return l.workerBatchSized(ctx, "http://127.0.0.1:18081", 30, 8000, "keda", 5*time.Minute)
	case "check":
		return l.kedaCheck(ctx)
	case "observe":
		observe, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			for _, args := range [][]string{{"get", "deployment/lab-worker", "-n", l.namespace, "-o", "json"}, {"get", "hpa/lab-worker-kafka-lag", "-n", l.namespace, "-o", "json"}} {
				out, err := l.kube(observe, args...)
				if err != nil {
					if observe.Err() != nil && ctx.Err() == nil {
						return nil
					}
					return err
				}
				if err := l.event("keda_observation", map[string]any{"snapshot": json.RawMessage(out)}); err != nil {
					return err
				}
			}
			select {
			case <-observe.Done():
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return nil
			case <-tick.C:
			}
		}
	}
	return nil
}

func (l lab) kedaWorker(ctx context.Context, enabled bool) error {
	ref, err := l.workerImage(ctx)
	if err != nil {
		return err
	}
	values, err := l.must(ctx, "helm", nil, "get", "values", "lab-worker", "--all", "-o", "json", "--namespace", l.namespace, "--kube-context", l.context)
	if err != nil {
		return err
	}
	var release struct {
		Lab   struct{ CrashBeforeOffsetJobID, DlqBootstrap string }
		Image struct{ Repository, Tag string }
	}
	if json.Unmarshal([]byte(values), &release) != nil || release.Lab.CrashBeforeOffsetJobID != "" || release.Lab.DlqBootstrap != "" || release.Image.Repository+":"+release.Image.Tag != ref {
		return errors.New("worker release must match the recorded image and have no armed fault")
	}
	if enabled {
		var cron struct {
			Spec   struct{ Suspend bool }
			Status struct{ Active []struct{ Name string } }
		}
		if err := l.getJSON(ctx, &cron, "get", "cronjob/lab-cron", "-n", l.namespace); err != nil {
			return err
		}
		if !cron.Spec.Suspend || len(cron.Status.Active) > 0 {
			return errors.New("suspend CronJob and finish its Jobs before the KEDA experiment")
		}
		var active struct {
			Items []struct{ Status struct{ Active int } }
		}
		if err := l.getJSON(ctx, &active, "get", "jobs", "-n", l.namespace, "-l", "app.kubernetes.io/name=lab-cron,app.kubernetes.io/instance=lab-cron"); err != nil {
			return err
		}
		for _, job := range active.Items {
			if job.Status.Active > 0 {
				return errors.New("finish manual CronJob lab Jobs before enabling KEDA")
			}
		}
		var scalers struct {
			Items []struct {
				Metadata struct{ Name string }
				Spec     struct{ ScaleTargetRef struct{ Name string } }
			}
		}
		if err := l.getJSON(ctx, &scalers, "get", "scaledobjects", "-n", l.namespace); err != nil {
			return err
		}
		for _, scaler := range scalers.Items {
			if scaler.Spec.ScaleTargetRef.Name == "lab-worker" && scaler.Metadata.Name != "lab-worker" {
				return errors.New("another ScaledObject controls lab-worker")
			}
		}
	}
	var hpas struct {
		Items []struct {
			Metadata struct{ Name string }
			Spec     struct{ ScaleTargetRef struct{ Name string } }
		}
	}
	if err := l.getJSON(ctx, &hpas, "get", "hpa", "-n", l.namespace); err != nil {
		return err
	}
	for _, hpa := range hpas.Items {
		if hpa.Spec.ScaleTargetRef.Name == "lab-worker" && hpa.Metadata.Name != "lab-worker-kafka-lag" {
			return errors.New("another HPA already controls lab-worker")
		}
	}
	upgrade := func(scaling, paused string) error {
		out, err := l.must(ctx, "helm", nil, "upgrade", "lab-worker", "helm/lab-worker", "--namespace", l.namespace, "--kube-context", l.context,
			"--reuse-values", "--set", "autoscaling.enabled="+scaling, "--set", "autoscaling.paused="+paused, "--set", "replicaCount=1", "--wait", "--timeout", "180s")
		l.print(out)
		return err
	}
	if enabled {
		if _, err := l.kube(ctx, "get", "secret/lab-keda-kafka", "-n", l.namespace, "-o", "name"); err != nil {
			return errors.New("create the dedicated KEDA Kafka Secret first")
		}
		if err := upgrade("true", "false"); err != nil {
			return err
		}
		out, err := l.kube(ctx, "wait", "--for=condition=Ready", "scaledobject/lab-worker", "-n", l.namespace, "--timeout=120s")
		l.print(out)
		return err
	}
	// Pause via Helm, wait for one replica, then remove the scaler/HPA before
	// reintroducing spec.replicas. Equal values avoid a forced ownership takeover.
	if err := upgrade("true", "true"); err != nil {
		return err
	}
	out, err := l.kube(ctx, "wait", "--for=jsonpath={.status.replicas}=1", "deployment/lab-worker", "-n", l.namespace, "--timeout=180s")
	l.print(out)
	if err != nil {
		return err
	}
	if out, err = l.kube(ctx, "delete", "scaledobject/lab-worker", "-n", l.namespace, "--wait=true", "--timeout=120s"); err != nil {
		return err
	}
	l.print(out)
	out, err = l.kube(ctx, "wait", "--for=delete", "hpa/lab-worker-kafka-lag", "-n", l.namespace, "--timeout=120s")
	l.print(out)
	if err != nil {
		return err
	}
	return upgrade("false", "false")
}

func (l lab) kedaCheck(ctx context.Context) error {
	body, err := os.ReadFile(".local-kafka/worker-batch.json")
	if err != nil {
		return err
	}
	var batch []jobs.Job
	if json.Unmarshal(body, &batch) != nil || len(batch) != 30 {
		return errors.New("expected the saved 30-job KEDA batch")
	}
	ids := make([]string, 0, len(batch))
	seen := map[string]bool{}
	for _, job := range batch {
		if !jobs.ValidID(job.ID) || seen[job.ID] {
			return errors.New("invalid or duplicate saved batch ID")
		}
		seen[job.ID] = true
		ids = append(ids, "'"+job.ID+"'::uuid")
	}
	// Only validated UUID literals enter SQL; no payloads or credentials printed.
	query := `SELECT j.id,j.status,(SELECT count(*) FROM public.lab_outbox o WHERE o.job_id=j.id) AS outbox_rows,(SELECT count(*) FROM public.lab_job_effects e WHERE e.job_id=j.id) AS effects FROM public.lab_jobs j WHERE j.id IN (` + strings.Join(ids, ",") + `) ORDER BY j.id;`
	out, err := l.kube(ctx, "exec", "pod/lab-postgres-0", "-n", l.namespace, "--", "psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", "lab", "-d", "lab", "-c", query)
	if err != nil {
		return err
	}
	l.print(out)
	query = `SELECT client_addr,application_name,state,count(*) FROM pg_stat_activity WHERE datname='lab' GROUP BY client_addr,application_name,state ORDER BY client_addr,application_name,state; SHOW max_connections;`
	out, err = l.kube(ctx, "exec", "pod/lab-postgres-0", "-n", l.namespace, "--", "psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", "lab", "-d", "lab", "-c", query)
	l.print(out)
	return err
}
