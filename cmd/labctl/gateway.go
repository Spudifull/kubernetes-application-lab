package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func (l lab) gatewayPreflight(ctx context.Context) error {
	if l.context != netContext {
		return errors.New("gateway preflight requires network cluster")
	}
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	out, err := l.kube(ctx, "get", "crd", "gatewayclasses.gateway.networking.k8s.io", "--ignore-not-found", "-o", "name")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) == "" {
		l.print("No GatewayClass CRD exists yet; Envoy Gateway will install the pinned bundle.")
		return nil
	}
	var classes struct {
		Items []struct {
			Metadata objectMeta `json:"metadata"`
			Spec     struct {
				ControllerName string `json:"controllerName"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := l.getJSON(ctx, &classes, "get", "gatewayclass"); err != nil {
		return err
	}
	for _, c := range classes.Items {
		if c.Spec.ControllerName != "gateway.envoyproxy.io/gatewayclass-controller" {
			return fmt.Errorf("another Gateway implementation owns GatewayClass %s: %s", c.Metadata.Name, c.Spec.ControllerName)
		}
	}
	l.print(fmt.Sprintf("Existing GatewayClass resources use Envoy Gateway only (count=%d).", len(classes.Items)))
	return nil
}

func (l lab) routeCondition(ctx context.Context, name, kind, want string) error {
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return waitFor(deadline, time.Second, func() (bool, error) {
		var route struct {
			Status struct {
				Parents []struct {
					Conditions []condition `json:"conditions"`
				} `json:"parents"`
			} `json:"status"`
		}
		if err := l.getJSON(deadline, &route, "get", "httproute", name, "--namespace", mainNS); err != nil {
			return false, err
		}
		for _, p := range route.Status.Parents {
			for _, c := range p.Conditions {
				if c.Type == kind && c.Status == want {
					l.print(fmt.Sprintf("route=%s condition=%s status=%s reason=%s", name, kind, want, c.Reason))
					if err := l.event("gateway_route_condition", map[string]any{"route": name, "condition": kind, "status": c.Status, "reason": c.Reason}); err != nil {
						return false, err
					}
					return true, nil
				}
			}
		}
		return false, nil
	})
}

func gatewayHTTPClient(roots *x509.CertPool) *http.Client {
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 5 * time.Second, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	return &http.Client{Timeout: 8 * time.Second, Transport: transport}
}

func (l lab) probeHTTP(ctx context.Context, label, url, host string, expected []int, app bool, roots *x509.CertPool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if host != "" {
		req.Host = host
	}
	resp, err := gatewayHTTPClient(roots).Do(req)
	if err != nil {
		return fmt.Errorf("probe %s: %w", label, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return err
	}
	l.print(fmt.Sprintf("probe=%s status=%d x-lab-pod=%s body=%s", label, resp.StatusCode, resp.Header.Get("X-Lab-Pod"), strings.TrimSpace(string(body))))
	if err := l.event("gateway_http_probe", map[string]any{"probe": label, "status": resp.StatusCode, "pod": resp.Header.Get("X-Lab-Pod"), "body": strings.TrimSpace(string(body))}); err != nil {
		return err
	}
	match := false
	for _, code := range expected {
		if resp.StatusCode == code {
			match = true
		}
	}
	if !match {
		return fmt.Errorf("unexpected HTTP status %d for %s", resp.StatusCode, label)
	}
	hasPod := resp.Header.Get("X-Lab-Pod") != ""
	if hasPod != app {
		return fmt.Errorf("unexpected X-Lab-Pod presence for %s", label)
	}
	if app {
		var version struct {
			Revision string `json:"revision"`
		}
		if json.Unmarshal(body, &version) != nil || version.Revision == "" {
			return fmt.Errorf("missing version revision for %s", label)
		}
	}
	return nil
}

func (l lab) gatewayProbe(ctx context.Context, mode string) error {
	if l.context != netContext {
		return errors.New("gateway probe requires network cluster")
	}
	switch mode {
	case "http":
		if err := l.probeHTTP(ctx, "http_version", "http://127.0.0.1:18080/version", "api.lab.test", []int{200}, true, nil); err != nil {
			return err
		}
		if err := l.probeHTTP(ctx, "unknown_hostname", "http://127.0.0.1:18080/version", "unknown.lab.test", []int{404, 421}, false, nil); err != nil {
			return err
		}
		return l.probeHTTP(ctx, "unmatched_path", "http://127.0.0.1:18080/not-routed", "api.lab.test", []int{404}, false, nil)
	case "tls":
		cert, err := os.ReadFile(".local-gateway-certs/ca.crt")
		if err != nil {
			return err
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(cert) {
			return errors.New("invalid laboratory CA certificate")
		}
		url := "https://api.lab.test:18443/version"
		// Preserve hostname verification while directing only this test to localhost.
		client := gatewayHTTPClient(roots)
		client.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, "127.0.0.1:18443")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		closeErr := resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if resp.StatusCode != 200 || resp.Header.Get("X-Lab-Pod") == "" || !strings.Contains(string(body), `"revision"`) {
			return errors.New("verified HTTPS did not return application version")
		}
		l.print(fmt.Sprintf("probe=https_version_verified status=%d x-lab-pod=%s body=%s", resp.StatusCode, resp.Header.Get("X-Lab-Pod"), strings.TrimSpace(string(body))))
		if err := l.event("gateway_https_probe", map[string]any{"probe": "https_version_verified", "status": resp.StatusCode, "pod": resp.Header.Get("X-Lab-Pod"), "body": strings.TrimSpace(string(body))}); err != nil {
			return err
		}
		without := gatewayHTTPClient(nil)
		without.Transport.(*http.Transport).DialContext = client.Transport.(*http.Transport).DialContext
		unexpected, err := without.Do(req.Clone(ctx))
		if err == nil {
			_ = unexpected.Body.Close()
			return errors.New("HTTPS without laboratory CA unexpectedly succeeded")
		}
		if _, ok := errors.AsType[x509.UnknownAuthorityError](err); !ok {
			return fmt.Errorf("HTTPS failed for reason other than untrusted CA: %w", err)
		}
		l.print("probe=https_without_trust error=unknown_authority expected=true")
		if err := l.event("gateway_tls_rejection", map[string]any{"probe": "https_without_trust", "reason": "unknown_authority", "expected": true}); err != nil {
			return err
		}
		return nil
	case "negative":
		if err := l.routeCondition(ctx, "lab-missing-backend", "ResolvedRefs", "False"); err != nil {
			return err
		}
		if err := l.routeCondition(ctx, "lab-missing-listener", "Accepted", "False"); err != nil {
			return err
		}
		if err := l.probeHTTP(ctx, "missing_backend", "http://127.0.0.1:18080/missing-backend", "api.lab.test", []int{500, 502, 503}, false, nil); err != nil {
			return err
		}
		return l.probeHTTP(ctx, "missing_listener", "http://127.0.0.1:18080/missing-listener", "api.lab.test", []int{404}, false, nil)
	default:
		return fmt.Errorf("gateway probe mode must be http, tls, or negative; got %q", mode)
	}
}
