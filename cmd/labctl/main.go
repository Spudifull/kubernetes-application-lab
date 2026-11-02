// labctl runs bounded laboratory scenarios without depending on a host shell.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"
)

const (
	mainContext = "kind-kubernetes-application-lab"
	netContext  = "kind-kubernetes-application-lab-net"
	mainNS      = "kubernetes-application-lab"
	policyNS    = "kubernetes-application-lab-policy"
	apiSelector = "app.kubernetes.io/name=lab-api,app.kubernetes.io/instance=lab-api"
)

type result struct {
	out  string
	code int
}

type runner interface {
	run(context.Context, string, []string, []byte) (result, error)
}

type execRunner struct{}

func (execRunner) run(ctx context.Context, name string, args []string, input []byte) (result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if input != nil {
		cmd.Stdin = strings.NewReader(string(input))
	}
	output, err := cmd.CombinedOutput()
	if err == nil {
		return result{out: string(output)}, nil
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return result{out: string(output), code: exit.ExitCode()}, nil
	}
	return result{out: string(output), code: -1}, fmt.Errorf("start %s: %w", name, err)
}

type lab struct {
	runner    runner
	context   string
	namespace string
	out       io.Writer
}

func (l lab) command(ctx context.Context, name string, input []byte, args ...string) (result, error) {
	return l.runner.run(ctx, name, args, input)
}

func (l lab) must(ctx context.Context, name string, input []byte, args ...string) (string, error) {
	r, err := l.command(ctx, name, input, args...)
	if err != nil {
		return "", err
	}
	if r.code != 0 {
		return "", fmt.Errorf("%s %s exited %d: %s", name, strings.Join(args, " "), r.code, strings.TrimSpace(r.out))
	}
	return r.out, nil
}

func (l lab) kube(ctx context.Context, args ...string) (string, error) {
	a := append([]string{"--context", l.context}, args...)
	return l.must(ctx, "kubectl", nil, a...)
}

func (l lab) kubeInput(ctx context.Context, input []byte, args ...string) (string, error) {
	a := append([]string{"--context", l.context}, args...)
	return l.must(ctx, "kubectl", input, a...)
}

func (l lab) kubeResult(ctx context.Context, args ...string) (result, error) {
	a := append([]string{"--context", l.context}, args...)
	return l.command(ctx, "kubectl", nil, a...)
}

func (l lab) getJSON(ctx context.Context, dst any, args ...string) error {
	args = append(args, "-o", "json")
	out, err := l.kube(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(out), dst); err != nil {
		return fmt.Errorf("parse kubectl JSON: %w", err)
	}
	return nil
}

func (l lab) requireContext(ctx context.Context) error {
	out, err := l.must(ctx, "kubectl", nil, "config", "current-context")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != l.context {
		return fmt.Errorf("current Kubernetes context must be %s, got %q", l.context, strings.TrimSpace(out))
	}
	return nil
}

func (l lab) print(output string) {
	_, _ = io.WriteString(l.out, output)
	if !strings.HasSuffix(output, "\n") {
		_, _ = io.WriteString(l.out, "\n")
	}
}

func (l lab) event(kind string, value any) error {
	fields, ok := value.(map[string]any)
	if !ok {
		fields = map[string]any{"value": value}
	}
	fields["event"] = kind
	fields["observed_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	b, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(l.out, string(b)); err != nil {
		return err
	}
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := runCLI(ctx, os.Args[1:], execRunner{}, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "labctl:", err)
		os.Exit(1)
	}
}

func runCLI(ctx context.Context, args []string, r runner, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("provide a labctl subcommand")
	}
	command := args[0]
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	contextName := f.String("context", mainContext, "explicit kubectl context")
	namespace := f.String("namespace", mainNS, "target namespace")
	duration := f.Duration("duration", 0, "observation duration")
	action := f.String("action", "", "scenario action")
	mode := f.String("mode", "", "probe mode")
	key := f.String("key", "", "bounded database record key")
	value := f.String("value", "", "bounded database record value")
	baseURL := f.String("url", "http://127.0.0.1:18081", "local API port-forward for jobs")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", f.Args())
	}
	if !strings.HasPrefix(*contextName, "kind-kubernetes-application-lab") || (*contextName != mainContext && *contextName != netContext) {
		return fmt.Errorf("unsupported laboratory context %q", *contextName)
	}
	if *namespace != mainNS && *namespace != policyNS && *namespace != "envoy-gateway-system" {
		return fmt.Errorf("unsupported laboratory namespace %q", *namespace)
	}
	l := lab{runner: r, context: *contextName, namespace: *namespace, out: out}
	var bound time.Duration
	switch command {
	case "observe-hpa":
		bound = 16 * time.Minute
	case "observe-rollout":
		bound = 11 * time.Minute
	case "gateway-forward":
		bound = 30 * time.Minute
	case "network-policy-test":
		bound = 20 * time.Minute
	case "kafka-bootstrap":
		bound = 12 * time.Minute
	case "kafka-broker-outage":
		bound = 12 * time.Minute
	case "keda", "observability":
		bound = 12 * time.Minute
	default:
		bound = 6 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	switch command {
	case "alert-receiver-image-record":
		return l.receiverImageRecord(ctx)
	case "alerting-rules-test":
		return l.alertRulesTest(ctx)
	case "observe-hpa":
		return l.observeHPA(ctx, chooseDuration(*duration, 660*time.Second))
	case "observe-rollout":
		return l.observeRollout(ctx, chooseDuration(*duration, 210*time.Second))
	case "rollout-probes":
		return l.rolloutProbes(ctx)
	case "scheduling":
		return l.scheduling(ctx, *action)
	case "inflight-work":
		return l.inflightWork(ctx)
	case "revision":
		return l.revision(ctx)
	case "release-check":
		return l.releaseCheck(ctx)
	case "release-verify":
		return l.releaseVerify(ctx)
	case "publish-check":
		return l.publishCheck(false)
	case "publish-context":
		_, _, err := publicationContext()
		return err
	case "publish-receipt":
		return l.publishCheck(true)
	case "image-record":
		return l.imageRecord(ctx)
	case "deploy-image":
		return l.deployImage(ctx, *action)
	case "rollback":
		return l.rollback(ctx, *action)
	case "gateway-preflight":
		return l.gatewayPreflight(ctx)
	case "gateway-cert-create":
		return l.gatewayCertCreate(ctx)
	case "gateway-forward":
		return l.gatewayForward(ctx)
	case "gateway-probe":
		return l.gatewayProbe(ctx, *mode)
	case "network-policy-test":
		return l.networkPolicyTest(ctx)
	case "rbac-test":
		return l.rbacTest(ctx)
	case "pod-security-test":
		return l.podSecurityTest(ctx)
	case "db-preflight":
		return l.dbPreflight(ctx)
	case "kafka-preflight":
		return l.kafkaPreflight(ctx)
	case "kafka-broker-outage":
		return l.kafkaBrokerOutage(ctx, *action)
	case "publisher-image-record":
		return l.publisherImageRecord(ctx)
	case "worker-image-record":
		return l.workerImageRecord(ctx)
	case "worker":
		return l.workerAction(ctx, *action)
	case "worker-secret-create":
		return l.kafkaIdentitySecretCreate(ctx, "worker")
	case "worker-dlq-secret-create":
		return l.kafkaIdentitySecretCreate(ctx, "dlq-inspector")
	case "worker-reliability-status":
		return l.workerReliabilityStatus(ctx)
	case "worker-batch":
		return l.workerBatch(ctx, *baseURL)
	case "worker-diagnostic":
		return l.workerDiagnostic(ctx, *action, *key)
	case "publisher":
		return l.publisherAction(ctx, *action)
	case "publisher-secret-create":
		return l.publisherSecretCreate(ctx)
	case "outbox-status":
		return l.outboxStatus(ctx)
	case "jobs-probe":
		return l.jobsProbe(ctx, *baseURL, *action, *key, *duration)
	case "cron":
		return l.cronAction(ctx, *action, *key)
	case "keda":
		return l.kedaAction(ctx, *action)
	case "observability":
		return l.observabilityAction(ctx, *action, *key)
	case "kafka-strimzi":
		return l.kafkaStrimzi(ctx, *action)
	case "kafka-bootstrap":
		return l.kafkaBootstrap(ctx)
	case "kafka-status":
		return l.kafkaStatus(ctx)
	case "kafka-probe-image-record":
		return l.kafkaProbeImage(ctx)
	case "kafka-smoke":
		return l.kafkaSmoke(ctx, *action)
	case "kafka-smoke-cleanup":
		return l.kafkaSmokeCleanup(ctx)
	case "kafka-broker-restart":
		return l.kafkaBrokerRestart(ctx)
	case "db-secret-create":
		return l.dbSecretCreate(ctx)
	case "db-deploy":
		return l.dbDeploy(ctx)
	case "db-status":
		return l.dbStatus(ctx)
	case "db-migrate":
		return l.dbMigrate(ctx, *action)
	case "db-record":
		return l.dbRecord(ctx, *action, *key, *value)
	case "db-pod-restart":
		return l.dbPodRestart(ctx)
	case "db-failure":
		return l.dbFailure(ctx, *action)
	case "db-backup":
		return l.dbBackup(ctx)
	case "db-restore":
		return l.dbRestore(ctx, *key)
	case "db-restore-cleanup":
		return l.dbRestoreCleanup(ctx)
	case "db-bad-credentials":
		return l.dbBadCredentials(ctx, *action)
	default:
		return fmt.Errorf("unknown labctl command %q", command)
	}
}

func chooseDuration(given, fallback time.Duration) time.Duration {
	if given == 0 {
		return fallback
	}
	return given
}
