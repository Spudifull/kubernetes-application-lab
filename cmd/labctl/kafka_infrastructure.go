package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const kafkaNS = "kubernetes-application-lab-kafka"

const kafkaProbeRepository = "kubernetes-application-lab-kafka-probe"

var kafkaProbeRef = regexp.MustCompile(`^kubernetes-application-lab-kafka-probe:[a-f0-9]{64}$`)

func (l lab) kafkaGuard(ctx context.Context) error {
	if l.context != mainContext || l.namespace != mainNS {
		return errors.New("Kafka infrastructure commands require the main cluster and application namespace")
	}
	return l.requireContext(ctx)
}

func (l lab) kafkaStrimzi(ctx context.Context, action string) error {
	if action != "render" && action != "install" && action != "status" {
		return errors.New("Strimzi action must be render, install, or status")
	}
	if err := l.kafkaGuard(ctx); err != nil {
		return err
	}
	if action == "status" {
		for _, args := range [][]string{
			{"get", "deployment,pod", "-n", kafkaNS, "-l", "name=strimzi-cluster-operator", "-o", "wide"},
			{"logs", "deployment/strimzi-cluster-operator", "-n", kafkaNS, "--tail=80", "--timestamps"},
		} {
			out, err := l.kube(ctx, args...)
			if err != nil {
				return err
			}
			l.print(out)
		}
		return nil
	}
	args := []string{"template", "lab-strimzi", "strimzi-kafka-operator"}
	if action == "install" {
		if err := l.kafkaPreflight(ctx); err != nil {
			return err
		}
		if err := l.kafkaKubectlVersion(ctx); err != nil {
			return err
		}
		for _, extra := range [][]string{
			{"apply", "--dry-run=server", "-f", "cluster/kafka/namespace.yaml"},
			{"apply", "-f", "cluster/kafka/namespace.yaml"},
		} {
			out, err := l.kube(ctx, extra...)
			if err != nil {
				return err
			}
			l.print(out)
		}
		args = []string{"upgrade", "--install", "lab-strimzi", "strimzi-kafka-operator"}
	}
	args = append(args, "--repo", "https://strimzi.io/charts/", "--version", "1.3.0", "--namespace", kafkaNS,
		"--kube-context", l.context, "-f", "cluster/kafka/operator-values.yaml")
	if action == "install" {
		args = append(args, "--wait", "--timeout", "300s")
	}
	out, err := l.must(ctx, "helm", nil, args...)
	if err != nil {
		return err
	}
	l.print(out)
	return nil
}

// Refuse a known unsupported kubectl/server minor skew before installation.
func (l lab) kafkaKubectlVersion(ctx context.Context) error {
	client, err := l.must(ctx, "kubectl", nil, "version", "--client=true", "-o", "json")
	if err != nil {
		return err
	}
	server, err := l.kube(ctx, "get", "--raw=/version")
	if err != nil {
		return err
	}
	var c struct{ ClientVersion struct{ Major, Minor string } }
	var s struct{ Major, Minor string }
	if err := json.Unmarshal([]byte(client), &c); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(server), &s); err != nil {
		return err
	}
	return kafkaVersionSkew(c.ClientVersion.Major, c.ClientVersion.Minor, s.Major, s.Minor)
}

func kafkaVersionSkew(clientMajor, clientMinor, serverMajor, serverMinor string) error {
	c, e1 := strconv.Atoi(strings.TrimSuffix(clientMinor, "+"))
	s, e2 := strconv.Atoi(strings.TrimSuffix(serverMinor, "+"))
	if e1 != nil || e2 != nil || clientMajor != serverMajor || c-s > 1 || s-c > 1 {
		return fmt.Errorf("unsupported kubectl/server skew: client %s.%s, server %s.%s; use kubectl within one server minor version", clientMajor, clientMinor, serverMajor, serverMinor)
	}
	return nil
}

func (l lab) kafkaBootstrap(ctx context.Context) error {
	if err := l.kafkaGuard(ctx); err != nil {
		return err
	}
	if err := l.kafkaKubectlVersion(ctx); err != nil {
		return err
	}
	for _, file := range []string{"cluster/kafka/kafka.yaml", "cluster/kafka/topics.yaml", "cluster/kafka/users.yaml"} {
		for _, dry := range []bool{true, false} {
			args := []string{"apply", "-f", file}
			if dry {
				args = append(args, "--dry-run=server")
			}
			out, err := l.kube(ctx, args...)
			if err != nil {
				return err
			}
			l.print(out)
		}
	}
	for _, target := range []string{"kafka/lab-kafka", "kafkatopic/lab-jobs-v1", "kafkatopic/lab-jobs-dlq-v1", "kafkatopic/lab-smoke-v1", "kafkauser/lab-kafka-smoke", "kafkauser/lab-publisher", "kafkauser/lab-worker", "kafkauser/lab-dlq-inspector"} {
		out, err := l.kube(ctx, "wait", "--for=condition=Ready", target, "-n", kafkaNS, "--timeout=300s")
		if err != nil {
			return err // keep failed resources and logs for inspection
		}
		l.print(out)
	}
	return l.kafkaStatus(ctx)
}

func (l lab) kafkaStatus(ctx context.Context) error {
	if err := l.kafkaGuard(ctx); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"get", "kafka,kafkanodepool,kafkatopic,kafkauser", "-n", kafkaNS, "-o", "yaml"},
		{"get", "pod,pvc,service", "-n", kafkaNS, "-o", "wide"},
		{"get", "pods", "-n", kafkaNS, "-l", "strimzi.io/name=lab-kafka-kafka", "-o", "jsonpath={range .items[*]}{.metadata.name}{\" UID=\"}{.metadata.uid}{\" imageID=\"}{.status.containerStatuses[*].imageID}{\"\\n\"}{end}"},
		{"get", "events", "-n", kafkaNS, "--sort-by=.lastTimestamp"},
	} {
		out, err := l.kube(ctx, args...)
		if err != nil {
			return err
		}
		l.print(out)
	}
	return nil
}

func (l lab) kafkaProbeImage(ctx context.Context) error {
	id, err := l.must(ctx, "docker", nil, "image", "inspect", "--format", "{{.Id}}", kafkaProbeRepository+":build")
	if err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	ref := kafkaProbeRepository + ":" + strings.TrimPrefix(id, "sha256:")
	if !strings.HasPrefix(id, "sha256:") || !kafkaProbeRef.MatchString(ref) {
		return errors.New("invalid Kafka probe image ID")
	}
	if _, err := l.must(ctx, "docker", nil, "image", "tag", kafkaProbeRepository+":build", ref); err != nil {
		return err
	}
	if err := os.MkdirAll(".local-kafka", 0700); err != nil {
		return err
	}
	if err := os.WriteFile(".local-kafka/probe-image-ref", []byte(ref+"\n"), 0600); err != nil {
		return err
	}
	l.print("Recorded Kafka probe image: " + ref)
	return nil
}

type kafkaSmokeLocator struct {
	Event     string `json:"event"`
	Key       string `json:"key"`
	Topic     string `json:"topic"`
	Partition int32  `json:"partition"`
	Offset    int64  `json:"offset"`
}

func (l lab) kafkaSmoke(ctx context.Context, action string) error {
	if action == "" {
		action = "roundtrip"
	}
	if action != "roundtrip" && action != "write" && action != "read" {
		return errors.New("Kafka smoke action must be roundtrip, write, or read")
	}
	if err := l.kafkaGuard(ctx); err != nil {
		return err
	}
	image, err := os.ReadFile(".local-kafka/probe-image-ref")
	if err != nil {
		return fmt.Errorf("build Kafka probe image first: %w", err)
	}
	ref := strings.TrimSpace(string(image))
	if !kafkaProbeRef.MatchString(ref) {
		return errors.New("invalid recorded Kafka probe image reference")
	}
	id, err := l.must(ctx, "docker", nil, "image", "inspect", "--format", "{{.Id}}", ref)
	if err != nil {
		return err
	}
	if strings.TrimSpace(id) != "sha256:"+strings.TrimPrefix(ref, kafkaProbeRepository+":") {
		return errors.New("Kafka probe image tag does not match image ID")
	}
	load, err := l.must(ctx, "kind", nil, "load", "docker-image", ref, "--name", "kubernetes-application-lab")
	if err != nil {
		return err
	}
	l.print(load)
	key := "smoke-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	args := []string{"-mode", action, "-key", key}
	if action == "read" {
		b, err := os.ReadFile(".local-kafka/smoke-locator.json")
		if err != nil {
			return fmt.Errorf("run Kafka smoke write before retention read: %w", err)
		}
		var loc kafkaSmokeLocator
		if err := json.Unmarshal(b, &loc); err != nil || loc.Event != "kafka_smoke_written" || loc.Topic != "lab.smoke.v1" || loc.Partition != 0 || loc.Offset < 0 || !regexp.MustCompile(`^smoke-[a-z0-9-]{1,100}$`).MatchString(loc.Key) {
			return errors.New("invalid saved smoke locator")
		}
		args = []string{"-mode", "read", "-key", loc.Key, "-partition", "0", "-offset", strconv.FormatInt(loc.Offset, 10)}
	}
	name := "lab-kafka-smoke-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	p := kafkaSmokePod(name, ref, args)
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	out, err := l.kubeInput(ctx, b, "create", "-f", "-")
	if err != nil {
		return err
	}
	l.print(out)
	wait, waitErr := l.kube(ctx, "wait", "--for=jsonpath={.status.phase}=Succeeded", "pod/"+name, "-n", kafkaNS, "--timeout=90s")
	l.print(wait)
	logs, logErr := l.kube(ctx, "logs", "pod/"+name, "-n", kafkaNS)
	if logErr == nil {
		l.print(logs)
	}
	if waitErr != nil || logErr != nil {
		return errors.Join(waitErr, logErr)
	}
	if action != "read" {
		var loc kafkaSmokeLocator
		found := false
		for _, line := range strings.Split(logs, "\n") {
			if json.Unmarshal([]byte(line), &loc) == nil && loc.Event == "kafka_smoke_written" && loc.Key == key && loc.Topic == "lab.smoke.v1" && loc.Partition == 0 && loc.Offset >= 0 {
				found = true
				break
			}
		}
		if !found {
			return errors.New("smoke exited without matching acknowledged record locator")
		}
		data, err := json.Marshal(loc)
		if err != nil {
			return err
		}
		if err := os.WriteFile(".local-kafka/smoke-locator.json", data, 0600); err != nil {
			return err
		}
	}
	l.print("Diagnostic Pod retained: " + name + "; cleanup explicitly after saving logs. Smoke retention is one hour.")
	return nil
}

func kafkaSmokePod(name, image string, args []string) map[string]any {
	return map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name, "namespace": kafkaNS, "labels": map[string]string{"app.kubernetes.io/name": "lab-kafka-smoke"}},
		"spec": map[string]any{
			"restartPolicy": "Never", "activeDeadlineSeconds": 75, "automountServiceAccountToken": false,
			"securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": 65532, "runAsGroup": 65532, "seccompProfile": map[string]string{"type": "RuntimeDefault"}},
			"containers": []any{map[string]any{
				"name": "probe", "image": image, "imagePullPolicy": "Never", "args": args,
				"resources":       map[string]any{"requests": map[string]string{"cpu": "25m", "memory": "32Mi"}, "limits": map[string]string{"cpu": "250m", "memory": "128Mi"}},
				"securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []string{"ALL"}}},
				"volumeMounts":    []any{map[string]any{"name": "ca", "mountPath": "/tls/ca", "readOnly": true}, map[string]any{"name": "user", "mountPath": "/tls/user", "readOnly": true}},
			}},
			"volumes": []any{
				map[string]any{"name": "ca", "secret": map[string]any{"secretName": "lab-kafka-cluster-ca-cert", "items": []any{map[string]string{"key": "ca.crt", "path": "ca.crt"}}}},
				map[string]any{"name": "user", "secret": map[string]any{"secretName": "lab-kafka-smoke", "items": []any{map[string]string{"key": "user.crt", "path": "user.crt"}, map[string]string{"key": "user.key", "path": "user.key"}}}},
			},
		},
	}
}

func (l lab) kafkaSmokeCleanup(ctx context.Context) error {
	if err := l.kafkaGuard(ctx); err != nil {
		return err
	}
	out, err := l.kube(ctx, "delete", "pods", "-n", kafkaNS, "-l", "app.kubernetes.io/name=lab-kafka-smoke", "--ignore-not-found=true", "--wait=true")
	if err != nil {
		return err
	}
	l.print(out)
	return nil
}

type kafkaPVCList struct {
	Items []struct {
		Metadata struct{ Name, UID string }
		Spec     struct{ VolumeName string }
		Status   struct{ Phase string }
	}
}

func kafkaPVCIdentity(list kafkaPVCList) (map[string]string, error) {
	ids := make(map[string]string)
	for _, pvc := range list.Items {
		if pvc.Metadata.Name == "" || pvc.Metadata.UID == "" || pvc.Spec.VolumeName == "" || pvc.Status.Phase != "Bound" {
			return nil, errors.New("Kafka PVC evidence incomplete or not Bound")
		}
		ids[pvc.Metadata.Name] = pvc.Metadata.UID + "/" + pvc.Spec.VolumeName
	}
	if len(ids) == 0 {
		return nil, errors.New("no persistent Kafka PVC found; refusing broker replacement")
	}
	return ids, nil
}

func (l lab) kafkaBrokerRestart(ctx context.Context) error {
	if err := l.kafkaGuard(ctx); err != nil {
		return err
	}
	if err := l.kafkaKubectlVersion(ctx); err != nil {
		return err
	}
	var pods struct {
		Items []struct{ Metadata struct{ Name, UID string } }
	}
	selector := "strimzi.io/name=lab-kafka-kafka"
	if err := l.getJSON(ctx, &pods, "get", "pods", "-n", kafkaNS, "-l", selector); err != nil {
		return err
	}
	if len(pods.Items) != 1 || pods.Items[0].Metadata.UID == "" {
		return errors.New("expected exactly one Kafka broker Pod; refusing replacement")
	}
	before := pods.Items[0].Metadata
	var pvcs kafkaPVCList
	if err := l.getJSON(ctx, &pvcs, "get", "pvc", "-n", kafkaNS, "-l", "strimzi.io/cluster=lab-kafka"); err != nil {
		return err
	}
	ids, err := kafkaPVCIdentity(pvcs)
	if err != nil {
		return err
	}
	var pool struct{ Status struct{ NodeIDs []int } }
	if err := l.getJSON(ctx, &pool, "get", "kafkanodepool", "dual-role", "-n", kafkaNS); err != nil {
		return err
	}
	if len(pool.Status.NodeIDs) != 1 {
		return errors.New("single Kafka node ID not observed; refusing replacement")
	}
	nodeID := pool.Status.NodeIDs[0]
	if err := l.event("kafka_broker_restart_before", map[string]any{"pod": before.Name, "uid": before.UID, "pvcs": ids, "node_id": nodeID}); err != nil {
		return err
	}
	out, err := l.kube(ctx, "delete", "pod", before.Name, "-n", kafkaNS, "--wait=false")
	if err != nil {
		return err
	}
	l.print(out)
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		var after struct {
			Items []struct {
				Metadata struct{ Name, UID, DeletionTimestamp string }
				Status   struct {
					Conditions []struct{ Type, Status string }
				}
			}
		}
		if err := l.getJSON(ctx, &after, "get", "pods", "-n", kafkaNS, "-l", selector); err != nil {
			return err
		}
		if len(after.Items) == 1 {
			p := after.Items[0]
			if p.Metadata.UID != "" && p.Metadata.UID != before.UID && p.Metadata.DeletionTimestamp == "" {
				for _, c := range p.Status.Conditions {
					if c.Type != "Ready" || c.Status != "True" {
						continue
					}
					if err := l.getJSON(ctx, &pvcs, "get", "pvc", "-n", kafkaNS, "-l", "strimzi.io/cluster=lab-kafka"); err != nil {
						return err
					}
					newIDs, err := kafkaPVCIdentity(pvcs)
					if err != nil {
						return err
					}
					if len(newIDs) != len(ids) {
						return errors.New("Kafka PVC set changed during replacement")
					}
					for name, id := range ids {
						if newIDs[name] != id {
							return errors.New("Kafka PVC UID or PV changed during replacement")
						}
					}
					if err := l.getJSON(ctx, &pool, "get", "kafkanodepool", "dual-role", "-n", kafkaNS); err != nil {
						return err
					}
					if len(pool.Status.NodeIDs) != 1 || pool.Status.NodeIDs[0] != nodeID {
						return errors.New("Kafka node identity changed")
					}
					return l.event("kafka_broker_restart_completed", map[string]any{"pod": p.Metadata.Name, "old_uid": before.UID, "new_uid": p.Metadata.UID, "pvcs": newIDs, "node_id": nodeID})
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for replacement Kafka broker: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
