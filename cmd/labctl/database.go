package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const dbSecretName = "lab-postgres-credentials"

func (l lab) dbPreflight(ctx context.Context) error {
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	var sc struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Provisioner, ReclaimPolicy, VolumeBindingMode string
	}
	if err := l.getJSON(ctx, &sc, "get", "storageclass", "standard"); err != nil {
		return fmt.Errorf("StorageClass standard unavailable; inspect available classes before deploying: %w", err)
	}
	if sc.Provisioner == "" {
		return errors.New("StorageClass has no provisioner")
	}
	return l.event("db_storage_preflight", map[string]any{"class": sc.Metadata.Name, "provisioner": sc.Provisioner, "reclaim_policy": sc.ReclaimPolicy, "volume_binding_mode": sc.VolumeBindingMode})
}

func (l lab) dbSecretCreate(ctx context.Context) error {
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	present, err := l.kubeResult(ctx, "get", "secret", dbSecretName, "--namespace", l.namespace, "-o", "name")
	if err != nil {
		return err
	}
	if present.code == 0 {
		l.print("Kubernetes Secret already exists; no credentials changed")
		return nil
	}
	if present.code != 1 {
		return errors.New("cannot determine Secret state")
	}
	path := filepath.Join(".local-db", "credentials.json")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(".local-db", 0700); err != nil {
			return err
		}
		passwordBytes := make([]byte, 32)
		if _, err := rand.Read(passwordBytes); err != nil {
			return err
		}
		password := base64.RawURLEncoding.EncodeToString(passwordBytes)
		secret := map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": dbSecretName, "namespace": l.namespace}, "type": "Opaque", "stringData": map[string]string{"username": "lab", "password": password}}
		b, err = json.Marshal(secret)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, b, 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	var secret struct {
		Metadata   struct{ Name, Namespace string } `json:"metadata"`
		StringData map[string]string                `json:"stringData"`
	}
	if err := json.Unmarshal(b, &secret); err != nil || secret.Metadata.Name != dbSecretName || secret.Metadata.Namespace != l.namespace || secret.StringData["username"] == "" || secret.StringData["password"] == "" {
		return errors.New("local credentials file is invalid; refusing Secret apply")
	}
	if _, err := l.kubeInput(ctx, b, "create", "-f", "-"); err != nil {
		return errors.New("Secret creation failed; credentials remain in ignored .local-db/credentials.json for recovery")
	}
	l.print("Created Kubernetes Secret from ignored .local-db/credentials.json; credentials were not printed")
	return nil
}

func (l lab) dbDeploy(ctx context.Context) error {
	if err := l.dbPreflight(ctx); err != nil {
		return err
	}
	if _, err := l.kube(ctx, "get", "secret", dbSecretName, "--namespace", l.namespace, "-o", "name"); err != nil {
		return errors.New("database credentials Secret missing; run task db-secret-create")
	}
	out, err := l.must(ctx, "helm", nil, "upgrade", "--install", "lab-postgres", "helm/lab-postgres", "--namespace", l.namespace, "--kube-context", l.context, "--wait", "--timeout", "180s")
	if err != nil {
		return err
	}
	l.print(out)
	return nil
}

func (l lab) dbStatus(ctx context.Context) error {
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"get", "statefulset,service,pod,pvc", "--namespace", l.namespace, "-l", "app.kubernetes.io/name=lab-postgres", "-o", "wide"},
		{"get", "pvc", "data-lab-postgres-0", "--namespace", l.namespace, "-o", "wide"},
		{"get", "events", "--namespace", l.namespace, "--field-selector", "involvedObject.name=lab-postgres-0", "--sort-by=.lastTimestamp"},
	} {
		out, err := l.kube(ctx, args...)
		if err != nil {
			return err
		}
		l.print(out)
	}
	return nil
}

func (l lab) dbImageRef() (string, error) {
	b, err := os.ReadFile(".local-image-ref")
	if err != nil {
		return "", err
	}
	ref := strings.TrimSpace(string(b))
	if _, err := validImageRef(ref); err != nil {
		return "", err
	}
	return ref, nil
}

func dbEnv() []any {
	return []any{
		map[string]any{"name": "LAB_DB_HOST", "value": "lab-postgres"},
		map[string]any{"name": "LAB_DB_PORT", "value": "5432"},
		map[string]any{"name": "LAB_DB_NAME", "valueFrom": map[string]any{"configMapKeyRef": map[string]any{"name": "lab-postgres-config", "key": "POSTGRES_DB"}}},
		map[string]any{"name": "LAB_DB_USER", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": dbSecretName, "key": "username"}}},
		map[string]any{"name": "LAB_DB_PASSWORD", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": dbSecretName, "key": "password"}}},
	}
}

func (l lab) dbMigrate(ctx context.Context, action string) error {
	if action != "" && action != "fail" {
		return errors.New("db-migrate action must be empty or fail")
	}
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	ref, err := l.dbImageRef()
	if err != nil {
		return err
	}
	if _, err := l.must(ctx, "kind", nil, "load", "docker-image", ref, "--name", strings.TrimPrefix(l.context, "kind-")); err != nil {
		return err
	}
	name := "lab-db-migration-" + time.Now().UTC().Format("20060102150405")
	args := []string{"migrate"}
	if action == "fail" {
		args = append(args, "--fail-lab")
	}
	job := map[string]any{"apiVersion": "batch/v1", "kind": "Job", "metadata": map[string]any{"name": name, "namespace": l.namespace, "labels": map[string]string{"app.kubernetes.io/name": "lab-db-migration"}}, "spec": map[string]any{"backoffLimit": 0, "activeDeadlineSeconds": 90, "ttlSecondsAfterFinished": 86400, "template": map[string]any{"metadata": map[string]any{"labels": map[string]string{"app.kubernetes.io/name": "lab-db-migration"}}, "spec": map[string]any{"restartPolicy": "Never", "automountServiceAccountToken": false, "securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": 65532, "seccompProfile": map[string]any{"type": "RuntimeDefault"}}, "containers": []any{map[string]any{"name": "migrate", "image": ref, "imagePullPolicy": "Never", "args": args, "env": dbEnv(), "securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []string{"ALL"}}}, "resources": map[string]any{"requests": map[string]string{"cpu": "25m", "memory": "32Mi"}, "limits": map[string]string{"cpu": "250m", "memory": "128Mi"}}}}}}}}
	b, err := json.Marshal(job)
	if err != nil {
		return err
	}
	if _, err := l.kubeInput(ctx, b, "create", "-f", "-"); err != nil {
		return err
	}
	l.print("Migration Job: " + name)
	if action == "fail" {
		l.print("Expected to fail; inspect Job status and logs before cleanup.")
		return nil
	}
	out, err := l.kube(ctx, "wait", "--for=condition=Complete", "job/"+name, "--namespace", l.namespace, "--timeout", "100s")
	if err != nil {
		return fmt.Errorf("migration Job %s did not complete; inspect logs: %w", name, err)
	}
	l.print(out)
	return nil
}

var safeKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (l lab) dbRecord(ctx context.Context, action, key, value string) error {
	if !safeKey.MatchString(key) {
		return errors.New("KEY must be 1-64 ASCII letters, digits, - or _")
	}
	path := "/api/v1/namespaces/" + l.namespace + "/services/http:lab-api:80/proxy/records/" + key
	switch action {
	case "put":
		if len(value) > 512 {
			return errors.New("VALUE exceeds 512 bytes")
		}
		body, _ := json.Marshal(map[string]string{"value": value})
		out, err := l.kubeInput(ctx, body, "create", "--raw", path, "-f", "-")
		if err != nil {
			return err
		}
		if strings.TrimSpace(out) != "" {
			l.print(out)
		}
		l.print("Record stored: " + key)
		return nil
	case "get":
		out, err := l.kube(ctx, "get", "--raw", path)
		if err != nil {
			return err
		}
		l.print(out)
		return nil
	default:
		return errors.New("db-record action must be put or get")
	}
}

func (l lab) dbPodRestart(ctx context.Context) error {
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	var before struct {
		Metadata struct {
			UID string `json:"uid"`
		} `json:"metadata"`
	}
	if err := l.getJSON(ctx, &before, "get", "pod", "lab-postgres-0", "--namespace", l.namespace); err != nil {
		return err
	}
	if _, err := l.kube(ctx, "delete", "pod", "lab-postgres-0", "--namespace", l.namespace, "--wait=true"); err != nil {
		return err
	}
	deadline := time.NewTimer(180 * time.Second)
	defer deadline.Stop()
	for {
		var after struct {
			Metadata struct {
				UID string `json:"uid"`
			} `json:"metadata"`
			Status struct {
				Conditions []struct{ Type, Status string } `json:"conditions"`
			} `json:"status"`
		}
		if err := l.getJSON(ctx, &after, "get", "pod", "lab-postgres-0", "--namespace", l.namespace); err == nil && after.Metadata.UID != "" && after.Metadata.UID != before.Metadata.UID {
			for _, condition := range after.Status.Conditions {
				if condition.Type == "Ready" && condition.Status == "True" {
					l.print("PostgreSQL Pod replaced: " + before.Metadata.UID + " -> " + after.Metadata.UID)
					return l.dbStatus(ctx)
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("replacement PostgreSQL Pod did not become Ready within 180s")
		case <-time.After(2 * time.Second):
		}
	}
}

func (l lab) dbFailure(ctx context.Context, action string) error {
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	var replicas string
	switch action {
	case "start":
		replicas = "0"
	case "restore":
		replicas = "1"
	default:
		return errors.New("db-failure action must be start or restore")
	}
	out, err := l.kube(ctx, "scale", "statefulset/lab-postgres", "--namespace", l.namespace, "--replicas="+replicas)
	if err != nil {
		return err
	}
	l.print(out)
	if action == "restore" {
		out, err := l.kube(ctx, "rollout", "status", "statefulset/lab-postgres", "--namespace", l.namespace, "--timeout", "180s")
		if err != nil {
			return err
		}
		l.print(out)
	}
	return nil
}

func (l lab) dbBackup(ctx context.Context) error {
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(".local-db", 0700); err != nil {
		return err
	}
	path := filepath.Join(".local-db", "backup.dump")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("backup file already exists or cannot be created: %w", err)
	}
	cmd := exec.CommandContext(ctx, "kubectl", "--context", l.context, "exec", "pod/lab-postgres-0", "--namespace", l.namespace, "--", "pg_dump", "-U", "lab", "-d", "lab", "-Fc", "--no-owner", "--no-acl")
	cmd.Stdout = f
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("pg_dump failed: %w (%s)", err, stderr.String())
	}
	info, err := f.Stat()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if info.Size() < 100 {
		_ = os.Remove(path)
		return errors.New("backup unexpectedly small")
	}
	l.print(fmt.Sprintf("Backup saved outside Pod: %s (%d bytes); restore is still required", path, info.Size()))
	return nil
}

func (l lab) dbRestore(ctx context.Context, key string) error {
	if !safeKey.MatchString(key) {
		return errors.New("provide --key for an existing control record")
	}
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	path := filepath.Join(".local-db", "backup.dump")
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := l.kube(ctx, "exec", "pod/lab-postgres-0", "--namespace", l.namespace, "--", "createdb", "-U", "lab", "lab_restore"); err != nil {
		return fmt.Errorf("isolated restore database must not already exist: %w", err)
	}
	cmd := exec.CommandContext(ctx, "kubectl", "--context", l.context, "exec", "-i", "pod/lab-postgres-0", "--namespace", l.namespace, "--", "pg_restore", "-U", "lab", "-d", "lab_restore", "--exit-on-error", "--no-owner", "--no-acl")
	cmd.Stdin = f
	var output strings.Builder
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restore into isolated lab_restore failed: %w (%s)", err, output.String())
	}
	query := "SELECT value FROM lab_records WHERE key='" + key + "'"
	out, err := l.kube(ctx, "exec", "pod/lab-postgres-0", "--namespace", l.namespace, "--", "psql", "-U", "lab", "-d", "lab_restore", "-tAc", query)
	if err != nil {
		return fmt.Errorf("restored record verification failed: %w", err)
	}
	if strings.TrimSpace(out) == "" {
		return errors.New("control record absent in restored database")
	}
	l.print("Restored control record in isolated lab_restore: " + key)
	return nil
}

func (l lab) dbRestoreCleanup(ctx context.Context) error {
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	out, err := l.kube(ctx, "exec", "pod/lab-postgres-0", "--namespace", l.namespace, "--", "dropdb", "-U", "lab", "lab_restore")
	if err != nil {
		return err
	}
	l.print(out)
	l.print("Removed only isolated lab_restore database; primary database and PVC retained")
	return nil
}

func (l lab) dbBadCredentials(ctx context.Context, action string) error {
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	const release = "lab-api-badcred"
	const secretName = "lab-postgres-wrong-credentials"
	switch action {
	case "start":
		if status, err := l.command(ctx, "helm", nil, "status", release, "--namespace", l.namespace, "--kube-context", l.context); err != nil {
			return err
		} else if status.code == 0 {
			return errors.New("isolated bad-credentials release already exists; refusing overwrite")
		}
		if secret, err := l.kubeResult(ctx, "get", "secret", secretName, "--namespace", l.namespace, "-o", "name"); err != nil {
			return err
		} else if secret.code == 0 {
			return errors.New("isolated bad-credentials Secret already exists; refusing overwrite")
		}
		ref, err := l.dbImageRef()
		if err != nil {
			return err
		}
		tag, err := validImageRef(ref)
		if err != nil {
			return err
		}
		if _, err := l.must(ctx, "kind", nil, "load", "docker-image", ref, "--name", strings.TrimPrefix(l.context, "kind-")); err != nil {
			return err
		}
		random := make([]byte, 32)
		if _, err := rand.Read(random); err != nil {
			return err
		}
		secret := map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": secretName, "namespace": l.namespace}, "type": "Opaque", "stringData": map[string]string{"username": "lab", "password": base64.RawURLEncoding.EncodeToString(random)}}
		b, err := json.Marshal(secret)
		if err != nil {
			return err
		}
		if _, err := l.kubeInput(ctx, b, "create", "-f", "-"); err != nil {
			return errors.New("isolated Secret creation failed")
		}
		out, err := l.must(ctx, "helm", nil, "upgrade", "--install", release, "helm/lab-api", "--namespace", l.namespace, "--kube-context", l.context, "-f", "helm/lab-api/values-db.yaml", "--set-string", "database.secretName="+secretName, "--set-string", "image.repository="+imageRepository, "--set-string", "image.tag="+tag)
		if err != nil {
			return err
		}
		l.print(out)
		l.print("Isolated release created with wrong credentials; primary Secret and API release untouched")
		return nil
	case "status":
		selector := "app.kubernetes.io/name=lab-api,app.kubernetes.io/instance=" + release
		out, err := l.kube(ctx, "get", "pod", "--namespace", l.namespace, "-l", selector, "-o", "wide")
		if err != nil {
			return err
		}
		l.print(out)
		out, err = l.kube(ctx, "logs", "--namespace", l.namespace, "-l", selector, "--all-containers=true", "--tail=100", "--prefix=true")
		if err != nil {
			return err
		}
		l.print(out)
		return nil
	case "cleanup":
		if status, err := l.command(ctx, "helm", nil, "status", release, "--namespace", l.namespace, "--kube-context", l.context); err != nil {
			return err
		} else if status.code == 0 {
			out, err := l.must(ctx, "helm", nil, "uninstall", release, "--namespace", l.namespace, "--kube-context", l.context, "--wait", "--timeout", "90s")
			if err != nil {
				return err
			}
			l.print(out)
		}
		out, err := l.kube(ctx, "delete", "secret", secretName, "--namespace", l.namespace, "--ignore-not-found=true")
		if err != nil {
			return err
		}
		l.print(out)
		return nil
	default:
		return errors.New("db-bad-credentials action must be start, status or cleanup")
	}
}
