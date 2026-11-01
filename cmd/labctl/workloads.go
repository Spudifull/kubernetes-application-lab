package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

func clientPod(name, namespace, label string, args []string, cpuRequest, cpuLimit string, deadline int) map[string]any {
	spec := map[string]any{
		"restartPolicy": "Never", "automountServiceAccountToken": false,
		"securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": 65532, "runAsGroup": 65532, "seccompProfile": map[string]string{"type": "RuntimeDefault"}},
		"containers": []any{map[string]any{
			"name": "client", "image": "kubernetes-application-lab-client:local", "imagePullPolicy": "Never", "args": args,
			"securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []string{"ALL"}}},
			"resources":       map[string]any{"requests": map[string]string{"cpu": cpuRequest, "memory": "16Mi"}, "limits": map[string]string{"cpu": cpuLimit, "memory": "32Mi"}},
		}},
	}
	if deadline > 0 {
		spec["activeDeadlineSeconds"] = deadline
	}
	return map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": name, "namespace": namespace, "labels": map[string]string{"app.kubernetes.io/name": label}}, "spec": spec}
}

func (l lab) createPod(ctx context.Context, obj map[string]any, dryRun bool) (string, error) {
	if err := l.requireContext(ctx); err != nil {
		return "", err
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	args := []string{"create"}
	if dryRun {
		args = append(args, "--dry-run=server", "--validate=true")
	}
	args = append(args, "-f", "-")
	return l.kubeInput(ctx, b, args...)
}

func (l lab) scheduling(ctx context.Context, action string) error {
	if action != "validate" && action != "start" {
		return errors.New("scheduling action must be validate or start")
	}
	if l.context != mainContext || l.namespace != mainNS {
		return errors.New("scheduling only supports the main laboratory cluster and namespace")
	}
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	var nodes struct {
		Items []struct {
			Metadata objectMeta `json:"metadata"`
			Status   struct {
				Allocatable map[string]string `json:"allocatable"`
				Conditions  []condition       `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := l.getJSON(ctx, &nodes, "get", "nodes"); err != nil {
		return err
	}
	if len(nodes.Items) != 1 {
		return errors.New("scheduling experiment requires exactly one node")
	}
	ready := false
	for _, c := range nodes.Items[0].Status.Conditions {
		if c.Type == "Ready" && c.Status == "True" {
			ready = true
		}
	}
	if !ready {
		return errors.New("laboratory node is not Ready")
	}
	alloc := nodes.Items[0].Status.Allocatable["cpu"]
	milli, err := parseCPU(alloc)
	if err != nil {
		return err
	}
	if milli <= 0 || milli > 9_000_000_000_000_000 {
		return errors.New("invalid CPU allocatable")
	}
	request := fmt.Sprintf("%dm", milli+1000)
	existing, err := l.kube(ctx, "get", "pod", "lab-api-unschedulable", "--namespace", l.namespace, "--ignore-not-found=true", "-o", "name")
	if err != nil {
		return err
	}
	if strings.TrimSpace(existing) != "" {
		return errors.New("experiment Pod already exists; preserve evidence before cleanup")
	}
	p := clientPod("lab-api-unschedulable", l.namespace, "lab-scheduling-client", []string{"-mode=memory", "-memory-mib=1", "-memory-hold=1s"}, request, request, 90)
	l.print(fmt.Sprintf("Node: %s; CPU allocatable: %s; experiment request: %s.", nodes.Items[0].Metadata.Name, alloc, request))
	out, err := l.createPod(ctx, p, true)
	if err != nil {
		return err
	}
	l.print(out)
	if action == "start" {
		out, err = l.createPod(ctx, p, false)
		if err != nil {
			return err
		}
		l.print(out)
	}
	return nil
}

func parseCPU(q string) (int64, error) {
	if strings.HasSuffix(q, "m") {
		return strconv.ParseInt(strings.TrimSuffix(q, "m"), 10, 64)
	}
	v, err := strconv.ParseInt(q, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("unsupported CPU allocatable quantity %q: %w", q, err)
	}
	if v > math.MaxInt64/1000 || v < math.MinInt64/1000 {
		return 0, fmt.Errorf("CPU allocatable quantity %q overflows millicores", q)
	}
	return v * 1000, nil
}

type clientEvent struct {
	Event string `json:"event"`
	Value struct {
		RunID string `json:"run_id"`
		Pod   string `json:"pod"`
	} `json:"value"`
}
type serverEvent struct {
	Msg   string `json:"msg"`
	RunID string `json:"run_id"`
}

func hasClientEvent(logs, kind, runID, podName string) bool {
	for _, line := range strings.Split(logs, "\n") {
		var e clientEvent
		if json.Unmarshal([]byte(line), &e) == nil && e.Event == kind && e.Value.RunID == runID && (podName == "" || e.Value.Pod == podName) {
			return true
		}
	}
	return false
}
func hasServerEvent(logs, kind, runID string) bool {
	for _, line := range strings.Split(logs, "\n") {
		var e serverEvent
		if json.Unmarshal([]byte(line), &e) == nil && e.Msg == kind && e.RunID == runID {
			return true
		}
	}
	return false
}

func canDeleteWork(logs, runID string) error {
	if !hasServerEvent(logs, "work_started", runID) {
		return errors.New("matching work_started is absent; API Pod must not be deleted")
	}
	if hasServerEvent(logs, "work_completed", runID) {
		return errors.New("work completed before SIGTERM; API Pod must not be deleted")
	}
	return nil
}

func (l lab) rolloutProbes(ctx context.Context) error {
	if l.context != mainContext || l.namespace != mainNS {
		return errors.New("rollout probes require the main cluster")
	}
	pods, err := l.listAPIPods(ctx)
	if err != nil {
		return err
	}
	targets := readyPods(pods)
	if len(targets) != 2 {
		return fmt.Errorf("expected two Ready API Pods, found %d", len(targets))
	}
	batch := time.Now().UTC().Format("20060102150405000")
	for i, p := range targets {
		name := fmt.Sprintf("lab-api-rollout-probe-%s-%d", batch, i+1)
		args := []string{"-mode=probe", "-url=http://" + p.Status.PodIP + ":8080/", "-request-timeout=1s", "-duration=120s", "-run-id=" + name}
		out, err := l.createPod(ctx, clientPod(name, l.namespace, "lab-rollout-probe", args, "10m", "100m", 0), false)
		if err != nil {
			return err
		}
		l.print(out)
		out, err = l.kube(ctx, "wait", "--for=condition=Ready", "pod/"+name, "--namespace", l.namespace, "--timeout=30s")
		if err != nil {
			return err
		}
		l.print(out)
		readyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err = waitFor(readyCtx, 200*time.Millisecond, func() (bool, error) {
			logs, e := l.kube(readyCtx, "logs", "pod/"+name, "--namespace", l.namespace)
			if e != nil {
				return false, e
			}
			return hasClientEvent(logs, "probe_ready_seen", name, p.Metadata.Name), nil
		})
		cancel()
		if err != nil {
			return fmt.Errorf("probe %s did not confirm original Ready pod %s: %w", name, p.Metadata.Name, err)
		}
		l.print(fmt.Sprintf("Probe %s observes %s (%s); preserve its logs after rollout.", name, p.Metadata.Name, p.Status.PodIP))
	}
	return nil
}

func (l lab) inflightWork(ctx context.Context) error {
	if l.context != mainContext || l.namespace != mainNS {
		return errors.New("inflight work requires the main cluster")
	}
	pods, err := l.listAPIPods(ctx)
	if err != nil {
		return err
	}
	targets := readyPods(pods)
	if len(targets) == 0 {
		return errors.New("no Ready API Pod")
	}
	target := targets[0]
	runID := "work-" + time.Now().UTC().Format("20060102150405000")
	name := "lab-api-" + runID
	probeName := name + "-probe"
	started := time.Now().UTC().Format(time.RFC3339Nano)
	l.print("Run ID: " + runID)
	l.print(fmt.Sprintf("Target Pod: %s (%s)", target.Metadata.Name, target.Status.PodIP))
	probeArgs := []string{"-mode=probe", "-url=http://" + target.Status.PodIP + ":8080/", "-request-timeout=1s", "-run-id=" + runID}
	out, err := l.createPod(ctx, clientPod(probeName, l.namespace, "lab-work-client", probeArgs, "10m", "100m", 0), false)
	if err != nil {
		return err
	}
	l.print(out)
	out, err = l.kube(ctx, "wait", "--for=condition=Ready", "pod/"+probeName, "--namespace", l.namespace, "--timeout=30s")
	if err != nil {
		return err
	}
	l.print(out)
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err = waitFor(probeCtx, 100*time.Millisecond, func() (bool, error) {
		logs, e := l.kube(probeCtx, "logs", "pod/"+probeName, "--namespace", l.namespace)
		if e != nil {
			return false, e
		}
		return hasClientEvent(logs, "probe_ready_seen", runID, target.Metadata.Name), nil
	})
	cancel()
	if err != nil {
		return fmt.Errorf("probe did not observe Ready Pod; API Pod was not deleted: %w", err)
	}
	workArgs := []string{"-mode=work", "-url=http://" + target.Status.PodIP + ":8080/work?duration_ms=8000", "-request-timeout=15s", "-run-id=" + runID}
	out, err = l.createPod(ctx, clientPod(name, l.namespace, "lab-work-client", workArgs, "10m", "100m", 0), false)
	if err != nil {
		return err
	}
	l.print(out)
	workCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err = waitFor(workCtx, 100*time.Millisecond, func() (bool, error) {
		logs, e := l.kube(workCtx, "logs", "pod/"+target.Metadata.Name, "--namespace", l.namespace, "--since-time="+started)
		if e != nil {
			return false, e
		}
		return hasServerEvent(logs, "work_started", runID), nil
	})
	cancel()
	if err != nil {
		return fmt.Errorf("no matching work_started; API Pod was not deleted: %w", err)
	}
	l.print("Server confirmed work_started for " + runID + " before SIGTERM.")
	logs, err := l.kube(ctx, "logs", "pod/"+target.Metadata.Name, "--namespace", l.namespace, "--since-time="+started)
	if err != nil {
		return err
	}
	if err := canDeleteWork(logs, runID); err != nil {
		return err
	}
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	out, err = l.kube(ctx, "delete", "pod/"+target.Metadata.Name, "--namespace", l.namespace, "--wait=false")
	if err != nil {
		return err
	}
	l.print(out)
	l.print("SIGTERM requested for " + target.Metadata.Name + ". Waiting for the already accepted HTTP response.")
	workWait, workErr := l.kube(ctx, "wait", "--for=jsonpath={.status.phase}=Succeeded", "pod/"+name, "--namespace", l.namespace, "--timeout=30s")
	if workWait != "" {
		l.print(workWait)
	}
	workLogs, err := l.kube(ctx, "logs", "pod/"+name, "--namespace", l.namespace)
	if err != nil {
		return err
	}
	l.print(workLogs)
	if workErr != nil {
		return fmt.Errorf("work client did not succeed; Pod %s preserved: %w", name, workErr)
	}
	probeWait, probeErr := l.kube(ctx, "wait", "--for=jsonpath={.status.phase}=Succeeded", "pod/"+probeName, "--namespace", l.namespace, "--timeout=25s")
	if probeWait != "" {
		l.print(probeWait)
	}
	probeLogs, err := l.kube(ctx, "logs", "pod/"+probeName, "--namespace", l.namespace)
	if err != nil {
		return err
	}
	l.print(probeLogs)
	if probeErr != nil {
		return fmt.Errorf("shutdown probe did not succeed; Pod %s preserved: %w", probeName, probeErr)
	}
	l.print(fmt.Sprintf("Client Pods %s and %s remain for evidence; remove them explicitly after recording results.", name, probeName))
	return nil
}
