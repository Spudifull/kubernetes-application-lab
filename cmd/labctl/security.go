package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func (l lab) rbacTest(ctx context.Context) error {
	if l.context != netContext || l.namespace != policyNS {
		return errors.New("RBAC scenario requires the policy namespace in the network cluster")
	}
	identity := "system:serviceaccount:" + l.namespace + ":lab-rbac-reader"
	tests := []struct{ label, verb, resource, namespace, expected string }{
		{"own_pod_read", "get", "pods/lab-rbac-client", l.namespace, "yes"},
		{"secret_read_denied", "list", "secrets", l.namespace, "no"},
		{"deployment_change_denied", "patch", "deployments.apps", l.namespace, "no"},
		{"other_namespace_read_denied", "list", "pods", mainNS, "no"},
	}
	for _, test := range tests {
		r, err := l.kubeResult(ctx, "auth", "can-i", test.verb, test.resource, "--namespace", test.namespace, "--as", identity)
		if err != nil {
			return err
		}
		answer := strings.TrimSpace(r.out)
		expectedCode := 0
		if test.expected == "no" {
			expectedCode = 1
		}
		l.print(fmt.Sprintf("authorization=%s namespace=%s answer=%s exit_code=%d expected=%s", test.label, test.namespace, answer, r.code, test.expected))
		if err := l.event("rbac_authorization", map[string]any{"check": test.label, "namespace": test.namespace, "answer": answer, "exit_code": r.code, "expected": test.expected}); err != nil {
			return err
		}
		if r.code != expectedCode || (test.expected == "yes" && answer != "yes") || (test.expected == "no" && !strings.HasPrefix(answer, "no")) {
			return fmt.Errorf("unexpected RBAC result for %s", test.label)
		}
	}
	out, err := l.kube(ctx, "wait", "--for=jsonpath={.status.phase}=Succeeded", "pod/lab-rbac-client", "--namespace", l.namespace, "--timeout", "120s")
	if err != nil {
		return fmt.Errorf("RBAC client did not succeed; preserve Pod: %w", err)
	}
	l.print(out)
	out, err = l.kube(ctx, "logs", "pod/lab-rbac-client", "--namespace", l.namespace, "--timestamps")
	if err != nil {
		return fmt.Errorf("cannot read RBAC client evidence: %w", err)
	}
	l.print(out)
	return nil
}

func apiSecurity(spec podSpec) error {
	if spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken {
		return errors.New("ServiceAccount token automount not disabled")
	}
	if !spec.SecurityContext.RunAsNonRoot || spec.SecurityContext.RunAsUser != 65532 || spec.SecurityContext.RunAsGroup != 65532 || spec.SecurityContext.SeccompProfile.Type != "RuntimeDefault" {
		return errors.New("pod securityContext is not restricted")
	}
	var api *container
	for i := range spec.Containers {
		if spec.Containers[i].Name == "api" {
			if api != nil {
				return errors.New("multiple API containers")
			}
			api = &spec.Containers[i]
		}
	}
	if api == nil {
		return errors.New("API container absent")
	}
	s := api.SecurityContext
	if s.AllowPrivilegeEscalation == nil || *s.AllowPrivilegeEscalation || !s.ReadOnlyRootFilesystem {
		return errors.New("API container privilege or filesystem settings invalid")
	}
	all := false
	for _, v := range s.Capabilities.Drop {
		if v == "ALL" {
			all = true
		}
	}
	if !all {
		return errors.New("API does not drop all capabilities")
	}
	for _, v := range spec.Volumes {
		if v.Projected != nil {
			for _, source := range v.Projected.Sources {
				if len(source.ServiceAccountToken) > 0 {
					return errors.New("projected ServiceAccount token volume found")
				}
			}
		}
	}
	return nil
}

func (l lab) podSecurityTest(ctx context.Context) error {
	if l.context != netContext || l.namespace != policyNS {
		return errors.New("Pod Security scenario requires the policy namespace in the network cluster")
	}
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	var ns struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err := l.getJSON(ctx, &ns, "get", "namespace", l.namespace); err != nil {
		return err
	}
	if ns.Metadata.Labels["pod-security.kubernetes.io/enforce"] != "restricted" {
		return errors.New("namespace does not enforce restricted")
	}
	l.print(fmt.Sprintf("namespace=%s enforce=restricted context=%s", l.namespace, l.context))
	var d deployment
	if err := l.getJSON(ctx, &d, "get", "deployment", "lab-api", "--namespace", l.namespace); err != nil {
		return err
	}
	if err := apiSecurity(d.Spec.Template.Spec); err != nil {
		return fmt.Errorf("Deployment template: %w", err)
	}
	pods, err := l.listAPIPods(ctx)
	if err != nil {
		return err
	}
	ready := readyPods(pods)
	if len(ready) == 0 {
		return errors.New("no Ready API Pod")
	}
	if err := apiSecurity(ready[0].Spec); err != nil {
		return fmt.Errorf("Pod/%s: %w", ready[0].Metadata.Name, err)
	}
	l.print(fmt.Sprintf("api_pod=%s ready=true template_security=restricted pod_security=restricted", ready[0].Metadata.Name))
	for _, name := range []string{"lab-pss-allowed", "lab-pss-hostpid"} {
		out, err := l.kube(ctx, "get", "pod", name, "--namespace", l.namespace, "--ignore-not-found=true", "-o", "name")
		if err != nil {
			return err
		}
		if strings.TrimSpace(out) != "" {
			return fmt.Errorf("Pod/%s already exists; preserve evidence", name)
		}
	}
	r, err := l.kubeResult(ctx, "create", "--dry-run=server", "--validate=true", "-f", "scripts/kubernetes/pod-security-hostpid.yaml")
	if err != nil {
		return err
	}
	l.print(fmt.Sprintf("admission=hostpid_dry_run exit_code=%d detail=%s", r.code, strings.TrimSpace(r.out)))
	if err := l.event("pod_security_admission", map[string]any{"check": "hostpid_dry_run", "exit_code": r.code, "expected_denial": isExpectedPSADenial(r), "detail": strings.TrimSpace(r.out)}); err != nil {
		return err
	}
	if !isExpectedPSADenial(r) {
		return errors.New("expected PSA refusal for hostPID; no positive Pod created")
	}
	out, err := l.kube(ctx, "get", "pod", "lab-pss-hostpid", "--namespace", l.namespace, "--ignore-not-found=true", "-o", "name")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "" {
		return errors.New("unsafe Pod exists after dry-run")
	}
	for _, args := range [][]string{{"create", "--dry-run=server", "--validate=true", "-f", "scripts/kubernetes/pod-security-allowed.yaml"}, {"create", "-f", "scripts/kubernetes/pod-security-allowed.yaml"}, {"wait", "--for=jsonpath={.status.phase}=Succeeded", "pod/lab-pss-allowed", "--namespace", l.namespace, "--timeout", "90s"}, {"logs", "pod/lab-pss-allowed", "--namespace", l.namespace, "--timestamps"}} {
		out, err := l.kube(ctx, args...)
		if err != nil {
			return err
		}
		l.print(out)
	}
	l.print("PASS: restricted admission rejected the hostPID dry-run and admitted the bounded compliant Pod. Preserve pod-security-status before cleanup.")
	return nil
}

func isExpectedPSADenial(r result) bool {
	return r.code != 0 && strings.Contains(r.out, `violates PodSecurity "restricted`) && strings.Contains(r.out, "host namespaces") && strings.Contains(r.out, "hostPID")
}
