package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const imageRepository = "kubernetes-application-lab-api"

func (l lab) revision(ctx context.Context) error {
	sha, err := l.must(ctx, "git", nil, "rev-parse", "--short=12", "HEAD")
	if err != nil {
		return err
	}
	status, err := l.must(ctx, "git", nil, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return err
	}
	rev := strings.TrimSpace(sha)
	if strings.TrimSpace(status) != "" {
		rev += "-dirty"
	}
	_, err = fmt.Fprint(l.out, rev)
	return err
}

func validImageRef(ref string) (string, error) {
	prefix := imageRepository + ":"
	if !strings.HasPrefix(ref, prefix) {
		return "", errors.New("recorded image repository does not match this lab")
	}
	tag := strings.TrimPrefix(ref, prefix)
	if len(tag) != 64 {
		return "", errors.New("recorded image tag is not a full SHA-256 value")
	}
	if _, err := hex.DecodeString(tag); err != nil {
		return "", fmt.Errorf("recorded image tag is not hexadecimal: %w", err)
	}
	return tag, nil
}

func (l lab) imageRecord(ctx context.Context) error {
	id, err := l.must(ctx, "docker", nil, "image", "inspect", "--format", "{{.Id}}", imageRepository+":build")
	if err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if !strings.HasPrefix(id, "sha256:") {
		return errors.New("built image ID is missing")
	}
	ref := imageRepository + ":" + strings.TrimPrefix(id, "sha256:")
	if _, err := validImageRef(ref); err != nil {
		return err
	}
	if _, err := l.must(ctx, "docker", nil, "image", "tag", imageRepository+":build", ref); err != nil {
		return err
	}
	if err := os.WriteFile(".local-image-ref", []byte(ref+"\n"), 0600); err != nil {
		return err
	}
	l.print("Recorded image: " + ref)
	return nil
}

func (l lab) deployImage(ctx context.Context, profile string) error {
	if strings.HasPrefix(profile, "jobs") && (l.context != mainContext || l.namespace != mainNS) {
		return errors.New("jobs profile requires the main cluster and application namespace")
	}
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	file := map[string]string{"": "", "rollout": "helm/lab-api/values-rollout.yaml", "cpu-high": "helm/lab-api/values-cpu-high.yaml", "cpu-low": "helm/lab-api/values-cpu-low.yaml", "hpa": "helm/lab-api/values-hpa.yaml", "db": "helm/lab-api/values-db.yaml", "jobs": "helm/lab-api/values-jobs.yaml", "jobs-tracing": "helm/lab-api/values-jobs.yaml", "jobs-tracing-unavailable": "helm/lab-api/values-jobs.yaml"}
	values, ok := file[profile]
	if !ok {
		return fmt.Errorf("unknown Helm profile %q", profile)
	}
	b, err := os.ReadFile(".local-image-ref")
	if err != nil {
		return fmt.Errorf("read .local-image-ref: %w", err)
	}
	ref := strings.TrimSpace(string(b))
	tag, err := validImageRef(ref)
	if err != nil {
		return err
	}
	id, err := l.must(ctx, "docker", nil, "image", "inspect", "--format", "{{.Id}}", ref)
	if err != nil {
		return err
	}
	if strings.TrimSpace(id) != "sha256:"+tag {
		return errors.New("recorded image ID does not match tag")
	}
	l.print("Deploying image: " + ref)
	cluster := strings.TrimPrefix(l.context, "kind-")
	if out, err := l.must(ctx, "kind", nil, "load", "docker-image", ref, "--name", cluster); err != nil {
		return err
	} else if strings.TrimSpace(out) != "" {
		l.print(out)
	}
	args := []string{"upgrade", "--install", "lab-api", "helm/lab-api", "--namespace", l.namespace, "--kube-context", l.context, "--wait", "--timeout", "90s"}
	if values != "" {
		args = append(args, "-f", values)
	}
	if profile == "jobs-tracing" {
		args = append(args, "-f", "cluster/observability/application-tracing-values.yaml")
	}
	if profile == "jobs-tracing-unavailable" {
		args = append(args, "-f", "cluster/observability/application-tracing-unavailable-values.yaml")
	}
	args = append(args, "--set-string", "image.repository="+imageRepository, "--set-string", "image.tag="+tag)
	out, err := l.must(ctx, "helm", nil, args...)
	if err != nil {
		return err
	}
	l.print(out)
	return nil
}

func (l lab) rollback(ctx context.Context, revision string) error {
	if revision == "" {
		return errors.New("provide ROLLBACK_REVISION as a positive integer")
	}
	n, err := strconv.Atoi(revision)
	if err != nil || n <= 0 {
		return errors.New("ROLLBACK_REVISION must be a positive integer")
	}
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	out, err := l.must(ctx, "helm", nil, "rollback", "lab-api", revision, "--namespace", l.namespace, "--kube-context", l.context, "--wait", "--timeout", "90s")
	if err != nil {
		return err
	}
	l.print(out)
	return nil
}

func (l lab) gatewayCertCreate(ctx context.Context) error {
	if l.context != netContext {
		return errors.New("certificates are only for the dedicated network cluster")
	}
	dir := ".local-gateway-certs"
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("certificate directory already exists: %s", dir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	caKey, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return err
	}
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	ca := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Kubernetes Application Lab Test CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(30 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	caDER, err := x509.CreateCertificate(rand.Reader, &ca, &ca, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return err
	}
	serial, err = rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	server := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "api.lab.test"}, DNSNames: []string{"api.lab.test"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(30 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	serverDER, err := x509.CreateCertificate(rand.Reader, &server, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	blocks := map[string]*pem.Block{"ca.crt": {Type: "CERTIFICATE", Bytes: caDER}, "ca.key": {Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(caKey)}, "tls.crt": {Type: "CERTIFICATE", Bytes: serverDER}, "tls.key": {Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(serverKey)}}
	for name, block := range blocks {
		if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(block), 0600); err != nil {
			return err
		}
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	parsed, err := x509.ParseCertificate(serverDER)
	if err != nil {
		return err
	}
	if _, err := parsed.Verify(x509.VerifyOptions{DNSName: "api.lab.test", Roots: roots, CurrentTime: now}); err != nil {
		return err
	}
	l.print("Laboratory CA certificate: " + filepath.Join(dir, "ca.crt"))
	l.print("Server certificate: " + filepath.Join(dir, "tls.crt"))
	l.print("Private keys remain in ignored directory: " + dir)
	return nil
}

func (l lab) gatewayForward(ctx context.Context) error {
	if l.context != netContext {
		return errors.New("gateway forward requires network cluster")
	}
	if err := l.requireContext(ctx); err != nil {
		return err
	}
	var services struct {
		Items []struct {
			Metadata objectMeta `json:"metadata"`
			Spec     struct {
				Type  string `json:"type"`
				Ports []struct {
					Port int `json:"port"`
				} `json:"ports"`
			} `json:"spec"`
		} `json:"items"`
	}
	selector := "gateway.envoyproxy.io/owning-gateway-namespace=kubernetes-application-lab,gateway.envoyproxy.io/owning-gateway-name=lab-gateway"
	if err := l.getJSON(ctx, &services, "get", "service", "--namespace", "envoy-gateway-system", "--selector", selector); err != nil {
		return err
	}
	if len(services.Items) != 1 {
		return fmt.Errorf("expected one Envoy Service, found %d", len(services.Items))
	}
	s := services.Items[0]
	if s.Spec.Type != "ClusterIP" {
		return fmt.Errorf("unexpected Envoy Service type: %s", s.Spec.Type)
	}
	ports := map[int]bool{}
	for _, p := range s.Spec.Ports {
		ports[p.Port] = true
	}
	if !ports[80] || !ports[443] {
		return errors.New("Envoy Service must expose HTTP 80 and HTTPS 443")
	}
	l.print("Forwarding " + s.Metadata.Name + " on 127.0.0.1:18080 and 127.0.0.1:18443. Keep this window open.")
	cmd := exec.CommandContext(ctx, "kubectl", "--context", l.context, "--namespace", "envoy-gateway-system", "port-forward", "--address", "127.0.0.1", "service/"+s.Metadata.Name, "18080:80", "18443:443")
	cmd.Stdout = l.out
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
