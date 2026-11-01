package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

func (l lab) networkPolicyTest(ctx context.Context) error {
	if l.context != netContext || l.namespace != policyNS {
		return errors.New("NetworkPolicy scenario requires policy namespace in network cluster")
	}
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	if _, err := l.kube(ctx, "get", "node", "kubernetes-application-lab-net-control-plane", "-o", "name"); err != nil {
		return err
	}
	serviceIP, err := l.kube(ctx, "get", "service", "lab-api", "--namespace", l.namespace, "-o", "jsonpath={.spec.clusterIP}")
	if err != nil {
		return err
	}
	serviceIP = strings.TrimSpace(serviceIP)
	if net.ParseIP(serviceIP) == nil {
		return fmt.Errorf("invalid API Service IP %q", serviceIP)
	}
	for _, name := range []string{"lab-policy-allowed", "lab-policy-blocked", "lab-policy-egress"} {
		if _, err := l.kube(ctx, "wait", "--for=condition=Ready", "pod/"+name, "--namespace", l.namespace, "--timeout", "30s"); err != nil {
			return err
		}
	}
	existing, err := l.kube(ctx, "get", "networkpolicy", "--namespace", l.namespace, "-o", "name")
	if err != nil {
		return err
	}
	if strings.TrimSpace(existing) != "" {
		return fmt.Errorf("policies already exist; preserve previous evidence: %s", strings.TrimSpace(existing))
	}
	l.print(fmt.Sprintf("Policy test baseline: context=%s namespace=%s service_ip=%s", l.context, l.namespace, serviceIP))
	ready := func() error {
		var d deployment
		if err := l.getJSON(ctx, &d, "get", "deployment", "lab-api", "--namespace", l.namespace); err != nil {
			return err
		}
		if d.Status.ReadyReplicas != 1 {
			return errors.New("isolated API is not Ready")
		}
		var slices endpointSliceList
		if err := l.getJSON(ctx, &slices, "get", "endpointslice", "--namespace", l.namespace, "-l", "kubernetes.io/service-name=lab-api"); err != nil {
			return err
		}
		found := false
		for _, s := range slices.Items {
			for _, e := range s.Endpoints {
				if e.Conditions.Ready != nil && *e.Conditions.Ready {
					found = true
				}
			}
		}
		if !found {
			return errors.New("API EndpointSlice lacks a Ready endpoint")
		}
		l.print(fmt.Sprintf("time=%s api_ready=true endpoint_ready=true", time.Now().UTC().Format(time.RFC3339Nano)))
		return nil
	}
	host := "lab-api." + l.namespace + ".svc"
	probeHTTP := func(client, stage string, shouldPass bool) error {
		for i := 0; i < 8; i++ {
			args := []string{"exec", "pod/" + client, "--namespace", l.namespace, "--container", "client", "--", "curl", "--noproxy", "*", "--silent", "--show-error", "--connect-timeout", "2", "--max-time", "5", "--resolve", host + ":80:" + serviceIP, "-o", "/dev/null", "-w", "http_code=%{http_code} remote_ip=%{remote_ip}", "http://" + host + "/version"}
			r, err := l.kubeResult(ctx, args...)
			if err != nil {
				return err
			}
			detail := strings.TrimSpace(r.out)
			l.print(fmt.Sprintf("time=%s stage=%s source=%s target=%s:80 protocol=TCP exit=%d %s", time.Now().UTC().Format(time.RFC3339Nano), stage, client, host, r.code, detail))
			if strings.Contains(detail, "error: unable to upgrade connection") || strings.Contains(detail, "container not found") || strings.Contains(detail, "executable file not found") {
				return errors.New("kubectl exec client failed; not policy evidence")
			}
			passed, denied := classifyHTTPProbe(r)
			if err := l.event("network_http_probe", map[string]any{"stage": stage, "source": client, "target": host + ":80", "exit_code": r.code, "passed": passed, "denied": denied, "expected_allow": shouldPass, "detail": detail}); err != nil {
				return err
			}
			if (shouldPass && passed) || (!shouldPass && denied) {
				return nil
			}
			if err := sleepContext(ctx, time.Second); err != nil {
				return err
			}
		}
		return fmt.Errorf("unexpected HTTP result at %s; preserve clients and policies", stage)
	}
	probeDNS := func(stage string, shouldPass bool) error {
		name := host + ".cluster.local."
		for i := 0; i < 8; i++ {
			r, err := l.kubeResult(ctx, "exec", "pod/lab-policy-egress", "--namespace", l.namespace, "--container", "client", "--", "nslookup", "-type=a", "-timeout=2", "-retry=1", name)
			if err != nil {
				return err
			}
			detail := strings.TrimSpace(r.out)
			l.print(fmt.Sprintf("time=%s stage=%s source=lab-policy-egress target=%s protocol=DNS exit=%d %s", time.Now().UTC().Format(time.RFC3339Nano), stage, name, r.code, detail))
			if strings.Contains(detail, "executable file not found") || strings.Contains(detail, "nslookup: not found") || strings.Contains(detail, "error: unable to upgrade connection") || strings.Contains(detail, "container not found") {
				return errors.New("DNS client failed; not policy evidence")
			}
			passed := r.code == 0 && strings.Contains(detail, serviceIP)
			denied := r.code != 0 && !strings.Contains(detail, serviceIP)
			if err := l.event("network_dns_probe", map[string]any{"stage": stage, "source": "lab-policy-egress", "target": name, "exit_code": r.code, "passed": passed, "denied": denied, "expected_allow": shouldPass, "detail": detail}); err != nil {
				return err
			}
			if (shouldPass && passed) || (!shouldPass && denied) {
				return nil
			}
			if err := sleepContext(ctx, time.Second); err != nil {
				return err
			}
		}
		return fmt.Errorf("unexpected DNS result at %s; preserve clients and policies", stage)
	}
	apply := func(file string) error {
		if err := l.requireContext(ctx); err != nil {
			return err
		}
		out, err := l.kube(ctx, "apply", "-f", "scripts/kubernetes/"+file)
		if err != nil {
			return err
		}
		l.print(out)
		if err := l.event("network_policy_applied", map[string]any{"manifest": file}); err != nil {
			return err
		}
		return nil
	}
	if err := ready(); err != nil {
		return err
	}
	for _, step := range []func() error{func() error { return probeDNS("baseline_dns", true) }, func() error { return probeHTTP("lab-policy-allowed", "baseline_ingress_allowed_client", true) }, func() error { return probeHTTP("lab-policy-blocked", "baseline_ingress_other_client", true) }, func() error { return probeHTTP("lab-policy-egress", "baseline_egress_client", true) }} {
		if err := step(); err != nil {
			return err
		}
	}
	if err := apply("network-policy-ingress-deny.yaml"); err != nil {
		return err
	}
	if err := ready(); err != nil {
		return err
	}
	if err := probeHTTP("lab-policy-allowed", "ingress_default_deny_allowed_client", false); err != nil {
		return err
	}
	if err := probeHTTP("lab-policy-blocked", "ingress_default_deny_other_client", false); err != nil {
		return err
	}
	if err := apply("network-policy-ingress-allow.yaml"); err != nil {
		return err
	}
	if err := ready(); err != nil {
		return err
	}
	if err := probeHTTP("lab-policy-allowed", "ingress_allow_selected_client", true); err != nil {
		return err
	}
	if err := probeHTTP("lab-policy-blocked", "ingress_other_client_still_denied", false); err != nil {
		return err
	}
	if err := probeHTTP("lab-policy-egress", "egress_client_before_egress_policy", true); err != nil {
		return err
	}
	if err := apply("network-policy-egress-deny.yaml"); err != nil {
		return err
	}
	if err := ready(); err != nil {
		return err
	}
	if err := probeDNS("egress_default_deny_dns", false); err != nil {
		return err
	}
	if err := probeHTTP("lab-policy-egress", "egress_default_deny_http", false); err != nil {
		return err
	}
	if err := apply("network-policy-dns-allow.yaml"); err != nil {
		return err
	}
	if err := ready(); err != nil {
		return err
	}
	if err := probeDNS("egress_dns_allow", true); err != nil {
		return err
	}
	if err := probeHTTP("lab-policy-egress", "dns_allowed_http_still_denied", false); err != nil {
		return err
	}
	if err := apply("network-policy-api-allow.yaml"); err != nil {
		return err
	}
	if err := ready(); err != nil {
		return err
	}
	if err := probeHTTP("lab-policy-egress", "egress_api_allow", true); err != nil {
		return err
	}
	if err := probeHTTP("lab-policy-blocked", "other_client_still_denied", false); err != nil {
		return err
	}
	l.print("PASS: observed all policy transitions; resources are retained for network-policy-status.")
	return nil
}

func classifyHTTPProbe(r result) (passed, denied bool) {
	passed = r.code == 0 && strings.Contains(r.out, "http_code=200")
	denied = (r.code == 7 || r.code == 28 || r.code == 52) && strings.Contains(r.out, "http_code=000")
	return passed, denied
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
