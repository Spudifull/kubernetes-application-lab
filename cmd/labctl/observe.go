package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type objectMeta struct {
	Name              string  `json:"name"`
	ResourceVersion   string  `json:"resourceVersion"`
	DeletionTimestamp *string `json:"deletionTimestamp"`
}
type condition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason"`
	LastTransitionTime string `json:"lastTransitionTime"`
}
type container struct {
	Name            string `json:"name"`
	Image           string `json:"image"`
	SecurityContext struct {
		AllowPrivilegeEscalation *bool `json:"allowPrivilegeEscalation"`
		ReadOnlyRootFilesystem   bool  `json:"readOnlyRootFilesystem"`
		Capabilities             struct {
			Drop []string `json:"drop"`
		} `json:"capabilities"`
	} `json:"securityContext"`
}
type podSpec struct {
	Containers                   []container `json:"containers"`
	AutomountServiceAccountToken *bool       `json:"automountServiceAccountToken"`
	SecurityContext              struct {
		RunAsNonRoot   bool  `json:"runAsNonRoot"`
		RunAsUser      int64 `json:"runAsUser"`
		RunAsGroup     int64 `json:"runAsGroup"`
		SeccompProfile struct {
			Type string `json:"type"`
		} `json:"seccompProfile"`
	} `json:"securityContext"`
	Volumes []struct {
		Projected *struct {
			Sources []struct {
				ServiceAccountToken json.RawMessage `json:"serviceAccountToken"`
			} `json:"sources"`
		} `json:"projected"`
	} `json:"volumes"`
}
type pod struct {
	Metadata objectMeta `json:"metadata"`
	Spec     podSpec    `json:"spec"`
	Status   struct {
		Phase      string      `json:"phase"`
		PodIP      string      `json:"podIP"`
		Conditions []condition `json:"conditions"`
	} `json:"status"`
}
type podList struct {
	Items []pod `json:"items"`
}
type endpointSliceList struct {
	Items []struct {
		Metadata  objectMeta `json:"metadata"`
		Endpoints []struct {
			Addresses  []string `json:"addresses"`
			Conditions struct {
				Ready       *bool `json:"ready"`
				Serving     *bool `json:"serving"`
				Terminating *bool `json:"terminating"`
			} `json:"conditions"`
			TargetRef struct {
				Name string `json:"name"`
			} `json:"targetRef"`
		} `json:"endpoints"`
	} `json:"items"`
}
type deployment struct {
	Spec struct {
		Replicas *int `json:"replicas"`
		Template struct {
			Spec podSpec `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		ReadyReplicas     int `json:"readyReplicas"`
		AvailableReplicas int `json:"availableReplicas"`
	} `json:"status"`
}
type hpa struct {
	Status struct {
		CurrentReplicas int `json:"currentReplicas"`
		DesiredReplicas int `json:"desiredReplicas"`
		CurrentMetrics  []struct {
			Type     string `json:"type"`
			Resource struct {
				Name    string `json:"name"`
				Current struct {
					AverageUtilization *int `json:"averageUtilization"`
				} `json:"current"`
			} `json:"resource"`
		} `json:"currentMetrics"`
		Conditions []condition `json:"conditions"`
	} `json:"status"`
}

func readyCondition(p pod) (condition, bool) {
	for _, c := range p.Status.Conditions {
		if c.Type == "Ready" {
			return c, true
		}
	}
	return condition{}, false
}

func readyPods(list podList) []pod {
	var out []pod
	for _, p := range list.Items {
		c, ok := readyCondition(p)
		if ok && c.Status == "True" && p.Status.Phase == "Running" && p.Metadata.DeletionTimestamp == nil && p.Status.PodIP != "" {
			out = append(out, p)
		}
	}
	return out
}

func (l lab) listAPIPods(ctx context.Context) (podList, error) {
	var pods podList
	err := l.getJSON(ctx, &pods, "get", "pods", "--namespace", l.namespace, "-l", apiSelector)
	return pods, err
}

func (l lab) observeRollout(ctx context.Context, duration time.Duration) error {
	if duration < 10*time.Second || duration > 600*time.Second {
		return errors.New("rollout observation duration must be 10–600 seconds")
	}
	deadline := time.NewTimer(duration)
	defer deadline.Stop()
	for {
		pods, err := l.listAPIPods(ctx)
		if err != nil {
			return err
		}
		podValue := make([]map[string]any, 0, len(pods.Items))
		for _, p := range pods.Items {
			c, _ := readyCondition(p)
			var image string
			if len(p.Spec.Containers) > 0 {
				image = p.Spec.Containers[0].Image
			}
			podValue = append(podValue, map[string]any{"name": p.Metadata.Name, "deletion_timestamp": p.Metadata.DeletionTimestamp, "phase": p.Status.Phase, "ready": c.Status, "ready_transition_time": c.LastTransitionTime, "image": image})
		}
		if err := l.event("pod_snapshot", map[string]any{"pods": podValue}); err != nil {
			return err
		}
		var slices endpointSliceList
		if err := l.getJSON(ctx, &slices, "get", "endpointslice", "--namespace", l.namespace, "-l", "kubernetes.io/service-name=lab-api"); err != nil {
			return err
		}
		endpoints := make([]map[string]any, 0)
		for _, s := range slices.Items {
			for _, e := range s.Endpoints {
				endpoints = append(endpoints, map[string]any{"slice": s.Metadata.Name, "resource_version": s.Metadata.ResourceVersion, "pod": e.TargetRef.Name, "addresses": e.Addresses, "ready": e.Conditions.Ready, "serving": e.Conditions.Serving, "terminating": e.Conditions.Terminating})
			}
		}
		if err := l.event("endpointslice_snapshot", map[string]any{"endpoints": endpoints}); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return nil
		case <-time.After(time.Second):
		}
	}
}

func (l lab) observeHPA(ctx context.Context, duration time.Duration) error {
	if duration < 60*time.Second || duration > 900*time.Second {
		return errors.New("HPA observation duration must be 60–900 seconds")
	}
	deadline := time.NewTimer(duration)
	defer deadline.Stop()
	for {
		var h hpa
		if err := l.getJSON(ctx, &h, "get", "hpa", "lab-api", "--namespace", l.namespace); err != nil {
			return err
		}
		var d deployment
		if err := l.getJSON(ctx, &d, "get", "deployment", "lab-api", "--namespace", l.namespace); err != nil {
			return err
		}
		pods, err := l.listAPIPods(ctx)
		if err != nil {
			return err
		}
		var cpu *int
		for _, m := range h.Status.CurrentMetrics {
			if m.Type == "Resource" && m.Resource.Name == "cpu" {
				cpu = m.Resource.Current.AverageUtilization
				break
			}
		}
		conditions := make([]map[string]string, 0, len(h.Status.Conditions))
		for _, c := range h.Status.Conditions {
			conditions = append(conditions, map[string]string{"type": c.Type, "status": c.Status, "reason": c.Reason})
		}
		podValue := make([]map[string]any, 0, len(pods.Items))
		for _, p := range pods.Items {
			c, _ := readyCondition(p)
			podValue = append(podValue, map[string]any{"name": p.Metadata.Name, "phase": p.Status.Phase, "ready": c.Status, "deletion_timestamp": p.Metadata.DeletionTimestamp})
		}
		value := map[string]any{"hpa_current_replicas": h.Status.CurrentReplicas, "hpa_desired_replicas": h.Status.DesiredReplicas, "cpu_utilization_percent": cpu, "hpa_conditions": conditions, "deployment_spec_replicas": d.Spec.Replicas, "deployment_ready_replicas": d.Status.ReadyReplicas, "deployment_available_replicas": d.Status.AvailableReplicas, "pods": podValue}
		if err := l.event("hpa_snapshot", value); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return nil
		case <-time.After(10 * time.Second):
		}
	}
}

func waitFor(ctx context.Context, interval time.Duration, check func() (bool, error)) error {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		ok, err := check()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for condition: %w", ctx.Err())
		case <-tick.C:
		}
	}
}
