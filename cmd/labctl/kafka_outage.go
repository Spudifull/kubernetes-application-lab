package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"time"
)

const kafkaOutageFile = ".local-kafka/broker-outage.json"
const operatorName = "strimzi-cluster-operator"
const brokerName = "lab-kafka-dual-role-0"
const brokerSelector = "strimzi.io/name=lab-kafka-kafka"

type outageMeta struct {
	Name, UID, DeletionTimestamp string
	OwnerReferences              []struct{ Kind, Name, UID string }
}
type outagePod struct {
	Metadata outageMeta
	Spec     struct{ NodeName string }
	Status   struct {
		Conditions []struct{ Type, Status string }
	}
}
type outagePods struct{ Items []outagePod }
type outageOperator struct {
	Metadata outageMeta
	Spec     struct {
		Replicas int
		Selector struct{ MatchLabels map[string]string }
	}
}
type outagePool struct {
	Metadata outageMeta
	Spec     struct {
		Replicas int
		Roles    []string
	}
	Status struct{ NodeIDs []int }
}
type kafkaOutageState struct {
	Version          int               `json:"version"`
	Context          string            `json:"context"`
	Namespace        string            `json:"namespace"`
	StartedAt        time.Time         `json:"started_at"`
	OperatorUID      string            `json:"operator_uid"`
	OperatorReplicas int               `json:"operator_replicas"`
	KafkaUID         string            `json:"kafka_uid"`
	PoolUID          string            `json:"pool_uid"`
	PodSetUID        string            `json:"pod_set_uid"`
	BrokerUID        string            `json:"broker_uid"`
	NodeID           int               `json:"node_id"`
	PVCs             map[string]string `json:"pvcs"`
}

func (s kafkaOutageState) validate() error {
	if s.Version != 1 || s.Context != mainContext || s.Namespace != kafkaNS || s.StartedAt.IsZero() || s.OperatorReplicas != 1 ||
		s.OperatorUID == "" || s.KafkaUID == "" || s.PoolUID == "" || s.PodSetUID == "" || s.BrokerUID == "" || s.NodeID < 0 || len(s.PVCs) != 1 {
		return errors.New("invalid broker outage recovery state; no resources changed")
	}
	if s.PVCs["data-0-"+brokerName] == "" {
		return errors.New("unexpected broker storage in recovery state")
	}
	return nil
}

func (l lab) outageOperator(ctx context.Context) (outageOperator, error) {
	var op outageOperator
	err := l.getJSON(ctx, &op, "get", "deployment", operatorName, "-n", kafkaNS)
	return op, err
}
func (l lab) outageStorage(ctx context.Context) (map[string]string, error) {
	var pvcs kafkaPVCList
	if err := l.getJSON(ctx, &pvcs, "get", "pvc", "-n", kafkaNS, "-l", "strimzi.io/cluster=lab-kafka"); err != nil {
		return nil, err
	}
	return kafkaPVCIdentity(pvcs)
}
func (l lab) outageMetadata(ctx context.Context, kind, name string) (outageMeta, error) {
	var obj struct{ Metadata outageMeta }
	err := l.getJSON(ctx, &obj, "get", kind, name, "-n", kafkaNS)
	return obj.Metadata, err
}

// Outage checks only known local resources. No StrimziPodSet spec, CR, PVC or
// topic is edited; the operator deployment is restored by the same scale path.
func (l lab) checkOutageIdentity(ctx context.Context, s kafkaOutageState) error {
	if err := s.validate(); err != nil {
		return err
	}
	op, err := l.outageOperator(ctx)
	if err != nil {
		return err
	}
	if op.Metadata.UID != s.OperatorUID || op.Metadata.DeletionTimestamp != "" || (op.Spec.Replicas != 0 && op.Spec.Replicas != 1) {
		return errors.New("operator identity or replica intent changed")
	}
	for _, item := range []struct{ kind, name, uid string }{{"kafka", "lab-kafka", s.KafkaUID}, {"kafkanodepool", "dual-role", s.PoolUID}, {"strimzipodset", "lab-kafka-dual-role", s.PodSetUID}} {
		m, err := l.outageMetadata(ctx, item.kind, item.name)
		if err != nil {
			return err
		}
		if m.UID != item.uid || m.DeletionTimestamp != "" {
			return errors.New("Kafka resource identity changed; refusing automatic recovery")
		}
	}
	var pool outagePool
	if err := l.getJSON(ctx, &pool, "get", "kafkanodepool", "dual-role", "-n", kafkaNS); err != nil {
		return err
	}
	if pool.Spec.Replicas != 1 || len(pool.Spec.Roles) != 2 || !containsRole(pool.Spec.Roles, "broker") || !containsRole(pool.Spec.Roles, "controller") || len(pool.Status.NodeIDs) != 1 || pool.Status.NodeIDs[0] != s.NodeID {
		return errors.New("Kafka node topology changed")
	}
	ids, err := l.outageStorage(ctx)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(ids, s.PVCs) {
		return errors.New("Kafka PVC UID or PV changed; retain recovery state and inspect storage")
	}
	return nil
}
func containsRole(roles []string, want string) bool {
	for _, role := range roles {
		if role == want {
			return true
		}
	}
	return false
}
func outagePodReady(p outagePod) bool {
	if p.Metadata.UID == "" || p.Metadata.DeletionTimestamp != "" {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == "Ready" && c.Status == "True" {
			return true
		}
	}
	return false
}

func saveOutageState(path string, s kafkaOutageState) error {
	if err := s.validate(); err != nil {
		return err
	}
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("outage state already exists or cannot be created; restore the prior outage first: %w", err)
	}
	_, writeErr := f.Write(body)
	syncErr := f.Sync()
	return errors.Join(writeErr, syncErr, f.Close())
}

func (l lab) kafkaBrokerOutage(ctx context.Context, action string) error {
	if action != "start" && action != "status" && action != "restore" {
		return errors.New("broker outage action must be start, status or restore")
	}
	if err := l.kafkaGuard(ctx); err != nil {
		return err
	}
	if err := l.kafkaKubectlVersion(ctx); err != nil {
		return err
	}
	if action == "start" {
		return l.startBrokerOutage(ctx)
	}
	if action == "status" {
		for _, args := range [][]string{
			{"get", "deployment", operatorName, "-n", kafkaNS, "-o", "wide"},
			{"get", "pods", "-n", kafkaNS, "-l", brokerSelector, "-o", "wide"},
			{"get", "pods", "-n", kafkaNS, "-l", "name=" + operatorName, "-o", "wide"},
			{"get", "pvc", "-n", kafkaNS, "-l", "strimzi.io/cluster=lab-kafka", "-o", "wide"},
			{"get", "endpointslice", "-n", kafkaNS, "-l", "kubernetes.io/service-name=lab-kafka-kafka-bootstrap", "-o", "yaml"},
		} {
			out, err := l.kube(ctx, args...)
			if err != nil {
				return err
			}
			l.print(out)
		}
		return nil
	}
	body, err := os.ReadFile(kafkaOutageFile)
	if err != nil {
		return fmt.Errorf("cannot read outage recovery state; inspect operator and use documented emergency recovery: %w", err)
	}
	var s kafkaOutageState
	if err := json.Unmarshal(body, &s); err != nil {
		return err
	}
	return l.restoreBrokerOutage(ctx, s)
}

func (l lab) startBrokerOutage(ctx context.Context) error {
	if _, err := os.Stat(kafkaOutageFile); !errors.Is(err, os.ErrNotExist) {
		return errors.New("active outage state exists or is inaccessible; restore it before another outage")
	}
	op, err := l.outageOperator(ctx)
	if err != nil {
		return err
	}
	if op.Spec.Replicas != 1 || op.Metadata.UID == "" || op.Metadata.DeletionTimestamp != "" {
		return errors.New("outage requires one configured operator replica")
	}
	if op.Spec.Selector.MatchLabels["name"] != operatorName {
		return errors.New("operator Pod selector differs from the expected chart; refusing outage")
	}
	var clusters struct {
		Items []struct{ Metadata outageMeta }
	}
	if err := l.getJSON(ctx, &clusters, "get", "kafka", "-n", kafkaNS); err != nil {
		return err
	}
	if len(clusters.Items) != 1 || clusters.Items[0].Metadata.Name != "lab-kafka" {
		return errors.New("outage requires only the laboratory Kafka cluster in the operator namespace")
	}
	if _, err := l.kube(ctx, "rollout", "status", "deployment/"+operatorName, "-n", kafkaNS, "--timeout=60s"); err != nil {
		return err
	}
	var pods outagePods
	if err := l.getJSON(ctx, &pods, "get", "pods", "-n", kafkaNS, "-l", brokerSelector); err != nil {
		return err
	}
	if len(pods.Items) != 1 || pods.Items[0].Metadata.Name != brokerName || !outagePodReady(pods.Items[0]) {
		return errors.New("outage requires exactly the known single Ready broker Pod")
	}
	k, err := l.outageMetadata(ctx, "kafka", "lab-kafka")
	if err != nil {
		return err
	}
	ps, err := l.outageMetadata(ctx, "strimzipodset", "lab-kafka-dual-role")
	if err != nil {
		return err
	}
	var pool outagePool
	if err := l.getJSON(ctx, &pool, "get", "kafkanodepool", "dual-role", "-n", kafkaNS); err != nil {
		return err
	}
	if len(pool.Status.NodeIDs) != 1 {
		return errors.New("expected exactly one Kafka node ID")
	}
	ids, err := l.outageStorage(ctx)
	if err != nil {
		return err
	}
	s := kafkaOutageState{Version: 1, Context: l.context, Namespace: kafkaNS, StartedAt: time.Now().UTC(), OperatorUID: op.Metadata.UID, OperatorReplicas: 1,
		KafkaUID: k.UID, PoolUID: pool.Metadata.UID, PodSetUID: ps.UID, BrokerUID: pods.Items[0].Metadata.UID, NodeID: pool.Status.NodeIDs[0], PVCs: ids}
	if err := l.checkOutageIdentity(ctx, s); err != nil {
		return err
	}
	owned := false
	for _, owner := range pods.Items[0].Metadata.OwnerReferences {
		if owner.Kind == "StrimziPodSet" && owner.Name == "lab-kafka-dual-role" && owner.UID == s.PodSetUID {
			owned = true
		}
	}
	if !owned {
		return errors.New("broker is not owned by the expected StrimziPodSet")
	}
	if err := os.MkdirAll(".local-kafka", 0700); err != nil {
		return err
	}
	if err := saveOutageState(kafkaOutageFile, s); err != nil {
		return err
	}
	l.print("Recovery state saved before mutations: " + kafkaOutageFile + "; use task kafka-broker-outage-restore even after a partial failure.")
	if err := l.event("kafka_broker_outage_before", map[string]any{"state": s}); err != nil {
		return err
	}
	out, err := l.kube(ctx, "scale", "deployment/"+operatorName, "-n", kafkaNS, "--current-replicas=1", "--replicas=0")
	if err != nil {
		return err
	}
	l.print(out)
	// Wait for every operator Pod to disappear before removing the broker.
	if err := l.waitOutagePodsGone(ctx, "name="+operatorName, 90*time.Second); err != nil {
		return err
	}
	op, err = l.outageOperator(ctx)
	if err != nil {
		return err
	}
	if op.Spec.Replicas != 0 {
		return errors.New("operator scale changed; refusing broker deletion")
	}
	if err := l.checkOutageIdentity(ctx, s); err != nil {
		return err
	}
	var broker outagePod
	if err := l.getJSON(ctx, &broker, "get", "pod", brokerName, "-n", kafkaNS); err != nil {
		return err
	}
	if broker.Metadata.UID != s.BrokerUID {
		return errors.New("broker Pod changed before deletion; restore operator and inspect")
	}
	out, err = l.kube(ctx, "delete", "pod", brokerName, "-n", kafkaNS, "--wait=true", "--timeout=90s")
	if err != nil {
		return err
	}
	l.print(out)
	if err := l.waitOutagePodsGone(ctx, brokerSelector, 15*time.Second); err != nil {
		return err
	}
	if err := l.checkOutageIdentity(ctx, s); err != nil {
		return err
	}
	op, err = l.outageOperator(ctx)
	if err != nil {
		return err
	}
	if op.Spec.Replicas != 0 {
		return errors.New("operator resumed during outage; inspect state and restore explicitly")
	}
	return l.event("kafka_broker_outage_started", map[string]any{"broker_pods": 0, "operator_replicas": 0, "pvcs": s.PVCs, "node_id": s.NodeID, "restore": "task kafka-broker-outage-restore"})
}

func (l lab) waitOutagePodsGone(ctx context.Context, selector string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		var pods outagePods
		if err := l.getJSON(ctx, &pods, "get", "pods", "-n", kafkaNS, "-l", selector); err != nil {
			return err
		}
		if len(pods.Items) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for Pods to disappear (%s): %w", selector, ctx.Err())
		case <-tick.C:
		}
	}
}

func (l lab) restoreBrokerOutage(ctx context.Context, s kafkaOutageState) error {
	if err := l.checkOutageIdentity(ctx, s); err != nil {
		return err
	}
	op, err := l.outageOperator(ctx)
	if err != nil {
		return err
	}
	if op.Spec.Replicas == 0 {
		out, err := l.kube(ctx, "scale", "deployment/"+operatorName, "-n", kafkaNS, "--current-replicas=0", "--replicas=1")
		if err != nil {
			return err
		}
		l.print(out)
	}
	l.print("Waiting up to 180s for Cluster Operator rollout.")
	rollout, err := l.kube(ctx, "rollout", "status", "deployment/"+operatorName, "-n", kafkaNS, "--timeout=180s")
	if err != nil {
		return err
	}
	l.print(rollout)
	l.print("Cluster Operator is ready; waiting up to 5m for the broker Pod. Recovery state remains saved until success.")
	wait, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	nextProgress := time.Now()
	for {
		var pods outagePods
		if err := l.getJSON(wait, &pods, "get", "pods", "-n", kafkaNS, "-l", brokerSelector); err != nil {
			return err
		}
		if len(pods.Items) == 1 && pods.Items[0].Metadata.Name == brokerName && outagePodReady(pods.Items[0]) {
			if err := l.checkOutageIdentity(wait, s); err != nil {
				return err
			}
			p := pods.Items[0]
			if err := l.event("kafka_broker_outage_restored", map[string]any{"old_uid": s.BrokerUID, "new_uid": p.Metadata.UID, "broker_replaced": s.BrokerUID != p.Metadata.UID, "operator_replicas": 1, "pvcs": s.PVCs, "node_id": s.NodeID}); err != nil {
				return err
			}
			archive := fmt.Sprintf(".local-kafka/broker-outage-restored-%d.json", time.Now().UnixNano())
			if err := os.Rename(kafkaOutageFile, archive); err != nil {
				return fmt.Errorf("operator and broker restored but state archive failed: %w", err)
			}
			l.print("Recovery snapshot retained: " + archive + ". Verify Kafka smoke, outbox and consumer offsets separately.")
			return nil
		}
		if !time.Now().Before(nextProgress) {
			if err := l.event("kafka_broker_restore_waiting", map[string]any{"broker_pods": len(pods.Items), "expected_pod": brokerName, "next_step_if_stalled": "inspect Cluster Operator logs and Kafka status; do not delete storage"}); err != nil {
				return err
			}
			nextProgress = time.Now().Add(10 * time.Second)
		}
		select {
		case <-wait.Done():
			return fmt.Errorf("wait for restored Kafka broker; state retained: %w", wait.Err())
		case <-tick.C:
		}
	}
}
