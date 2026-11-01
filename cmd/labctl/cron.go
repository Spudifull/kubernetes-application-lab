package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/scheduled"
)

func (l lab) cronAction(ctx context.Context, action, timestamp string) error {
	if action == "profile" {
		return l.cronProfile(ctx, timestamp)
	}
	if action == "run-status" {
		request, err := scheduled.Request(l.namespace, "lab-cron", timestamp)
		if err != nil {
			return err
		}
		if err := l.kafkaGuard(ctx); err != nil {
			return err
		}
		// The key is generated as cron_ plus SHA-256 hex, never raw input SQL.
		query := `SELECT count(*) AS jobs,(SELECT count(*) FROM public.lab_outbox o JOIN public.lab_jobs j ON j.id=o.job_id WHERE j.idempotency_key='` + request.IdempotencyKey + `') AS outbox_rows,(SELECT count(*) FROM public.lab_job_effects e JOIN public.lab_jobs j ON j.id=e.job_id WHERE j.idempotency_key='` + request.IdempotencyKey + `') AS effects FROM public.lab_jobs WHERE idempotency_key='` + request.IdempotencyKey + `';`
		out, err := l.kube(ctx, "exec", "pod/lab-postgres-0", "-n", l.namespace, "--", "psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", "lab", "-d", "lab", "-c", query)
		l.print(out)
		return err
	}
	switch action {
	case "deploy", "resume", "suspend", "status", "run", "cleanup":
	default:
		return errors.New("cron action must be deploy, resume, suspend, status, run or cleanup")
	}
	if action == "run" {
		if _, err := scheduled.Request(l.namespace, "lab-cron", timestamp); err != nil {
			return err
		}
	}
	if err := l.kafkaGuard(ctx); err != nil {
		return err
	}
	selector := "app.kubernetes.io/name=lab-cron,app.kubernetes.io/instance=lab-cron"
	switch action {
	case "deploy":
		ref, err := l.dbImageRef()
		if err != nil {
			return err
		}
		tag, err := validImageRef(ref)
		if err != nil {
			return err
		}
		id, err := l.must(ctx, "docker", nil, "image", "inspect", "--format", "{{.Id}}", ref)
		if err != nil {
			return err
		}
		if strings.TrimSpace(id) != "sha256:"+tag {
			return errors.New("cron API image ID does not match recorded tag")
		}
		rendered, err := l.must(ctx, "helm", nil, "template", "lab-cron", "helm/lab-cron", "--namespace", l.namespace,
			"--set", "enabled=true", "--set", "suspend=true", "--set-string", "image.tag="+tag)
		if err != nil {
			return err
		}
		if _, err := l.kubeInput(ctx, []byte(rendered), "apply", "--dry-run=server", "-n", l.namespace, "-f", "-"); err != nil {
			return err
		}
		if _, err := l.must(ctx, "kind", nil, "load", "docker-image", ref, "--name", "kubernetes-application-lab"); err != nil {
			return err
		}
		out, err := l.must(ctx, "helm", nil, "upgrade", "--install", "lab-cron", "helm/lab-cron", "--namespace", l.namespace, "--kube-context", l.context,
			"--reset-values", "--set", "enabled=true", "--set", "suspend=true", "--set-string", "image.tag="+tag, "--wait", "--timeout", "120s")
		l.print(out)
		return err
	case "resume", "suspend":
		suspend := "true"
		if action == "resume" {
			suspend = "false"
		}
		// Keep suspend owned by Helm, including on a later deploy. Do not add
		// another field manager that conflicts with server-side Helm upgrades.
		out, err := l.must(ctx, "helm", nil, "upgrade", "lab-cron", "helm/lab-cron", "--namespace", l.namespace, "--kube-context", l.context,
			"--reuse-values", "--set", "suspend="+suspend, "--wait", "--timeout", "120s")
		l.print(out)
		return err
	case "status":
		out, err := l.kube(ctx, "get", "cronjob,job,pod", "-n", l.namespace, "-l", selector, "-o", "wide")
		l.print(out)
		return err
	case "cleanup":
		// Disable new scheduled Jobs before deleting only this lab's Jobs.
		if err := l.cronAction(ctx, "suspend", ""); err != nil {
			return err
		}
		out, err := l.kube(ctx, "delete", "jobs", "-n", l.namespace, "-l", selector, "--wait=true", "--timeout=120s")
		l.print(out)
		return err
	case "run":
		// kubectl preserves the full Helm job template. Only the explicit replay
		// timestamp differs; automatic CronJob runs leave this override empty.
		name := fmt.Sprintf("lab-cron-manual-%d", time.Now().UnixNano())
		draft, err := l.kube(ctx, "create", "job", name, "--from=cronjob/lab-cron", "-n", l.namespace, "--dry-run=client", "-o", "json")
		if err != nil {
			return err
		}
		draft, err = l.must(ctx, "kubectl", []byte(draft), "--context", l.context, "set", "env", "--local", "-f", "-", "LAB_CRON_SCHEDULED_AT="+timestamp, "-o", "json")
		if err != nil {
			return err
		}
		out, err := l.must(ctx, "kubectl", []byte(draft), "--context", l.context, "create", "-n", l.namespace, "-f", "-")
		l.print(out)
		if err != nil {
			return err
		}
		l.print("Cron replay Job: " + name + "; retain logs before cleanup.")
		out, err = l.kube(ctx, "wait", "--for=condition=complete", "job/"+name, "-n", l.namespace, "--timeout=120s")
		l.print(out)
		if err != nil {
			return err
		}
		out, err = l.kube(ctx, "logs", "job/"+name, "-n", l.namespace, "--timestamps")
		l.print(out)
		return err
	}
	return nil
}

func (l lab) cronProfile(ctx context.Context, profile string) error {
	switch profile {
	case "normal", "forbid", "deadline", "db-failure":
	default:
		return errors.New("cron profile must be normal, forbid, deadline or db-failure")
	}
	if err := l.kafkaGuard(ctx); err != nil {
		return err
	}
	var cron struct {
		Spec   struct{ Suspend bool }
		Status struct{ Active []struct{ Name string } }
	}
	if err := l.getJSON(ctx, &cron, "get", "cronjob/lab-cron", "-n", l.namespace); err != nil {
		return err
	}
	if !cron.Spec.Suspend || len(cron.Status.Active) != 0 {
		return errors.New("suspend CronJob and wait for its active Jobs before changing profile; existing Jobs retain their old configuration")
	}
	var list struct {
		Items []struct{ Status struct{ Active int } }
	}
	if err := l.getJSON(ctx, &list, "get", "jobs", "-n", l.namespace, "-l", "app.kubernetes.io/name=lab-cron,app.kubernetes.io/instance=lab-cron"); err != nil {
		return err
	}
	for _, job := range list.Items {
		if job.Status.Active > 0 {
			return errors.New("active manual CronJob lab Job must finish before profile changes")
		}
	}
	out, err := l.must(ctx, "helm", nil, "upgrade", "lab-cron", "helm/lab-cron", "--namespace", l.namespace, "--kube-context", l.context,
		"--reuse-values", "-f", "helm/lab-cron/values-"+profile+".yaml", "--set", "suspend=true", "--wait", "--timeout", "120s")
	l.print(out)
	return err
}
