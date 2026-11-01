package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/kversion"
)

const metricsKafkaServer = "lab-kafka-kafka-bootstrap.kubernetes-application-lab-kafka.svc.cluster.local"

// Credentials stay in memory. Every broker connection goes through the owner's
// explicit loopback port-forward; TLS still verifies the laboratory DNS name.
func (e monitoringEvidence) dependencyACLCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	secret, err := e.dependencySecret(ctx, observabilityNS, "lab-metrics-kafka")
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(secret["ca.crt"]) {
		return errors.New("monitoring CA missing or invalid")
	}
	cert, err := tls.X509KeyPair(secret["user.crt"], secret["user.key"])
	if err != nil {
		return errors.New("monitoring TLS identity missing or invalid")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil || leaf.Subject.CommonName != "lab-metrics" {
		return errors.New("monitoring certificate must identify lab-metrics")
	}
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: &tls.Config{
		MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{cert}, ServerName: metricsKafkaServer,
	}}
	cl, err := kgo.NewClient(kgo.SeedBrokers(metricsKafkaServer+":9093"),
		kgo.DisableIdempotentWrite(), kgo.RequiredAcks(kgo.LeaderAck()),
		// Name-based request schemas simplify this bounded compatibility probe.
		kgo.MaxVersions(kversion.V3_8_0()), kgo.ClientID("lab-metrics-acl-check"),
		kgo.Dialer(func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", "127.0.0.1:19094")
		}), kgo.RequestRetries(0), kgo.RequestTimeoutOverhead(5*time.Second))
	if err != nil {
		return errors.New("cannot construct monitoring ACL client")
	}
	defer cl.Close()
	metadata := kmsg.NewPtrMetadataRequest()
	topic := "lab.jobs.v1"
	metadata.Topics = []kmsg.MetadataRequestTopic{{Topic: &topic}}
	metadata.AllowAutoTopicCreation = false
	m, err := metadata.RequestWith(ctx, cl)
	if err != nil {
		return errors.New("monitoring metadata probe failed; check Kafka port-forward on 19094 and TLS identity")
	}
	if len(m.Brokers) != 1 || len(m.Topics) != 1 || m.Topics[0].ErrorCode != 0 {
		return errors.New("monitoring ACL check requires one broker and authorized existing job topic metadata")
	}
	// Direct requests avoid producer routing rejecting partition -1 locally.
	// No group is joined, no producer ID allocated, no valid partition targeted.
	broker := cl.Broker(int(m.Brokers[0].NodeID))
	return runMonitoringACL(ctx, broker, func(probe string, code int16) error {
		body, err := json.Marshal(map[string]any{"event": "dependency_kafka_acl", "probe": probe,
			"observed_at": time.Now().UTC().Format(time.RFC3339Nano), "principal": "CN=lab-metrics",
			"partition": -1, "error_code": code, "reason": kerr.ErrorForCode(code).Error(), "expected": true})
		if err != nil {
			return err
		}
		if err := e.save(probe+".json", body); err != nil {
			return err
		}
		e.lab.print(string(body))
		return nil
	})
}

func monitoringACLRequests() (*kmsg.ProduceRequest, *kmsg.OffsetCommitRequest) {
	p := kmsg.NewPtrProduceRequest()
	p.Acks, p.TimeoutMillis = 1, 5000
	p.Topics = []kmsg.ProduceRequestTopic{{Topic: "lab.jobs.v1", Partitions: []kmsg.ProduceRequestTopicPartition{{Partition: -1, Records: []byte{}}}}}
	o := kmsg.NewPtrOffsetCommitRequest()
	o.Group = "lab-workers"
	part := kmsg.NewOffsetCommitRequestTopicPartition()
	part.Partition, part.Offset = -1, -1
	o.Topics = []kmsg.OffsetCommitRequestTopic{{Topic: "lab.jobs.v1", Partitions: []kmsg.OffsetCommitRequestTopicPartition{part}}}
	return p, o
}

func runMonitoringACL(ctx context.Context, broker kmsg.Requestor, emit func(string, int16) error) error {
	p, o := monitoringACLRequests()
	pr, err := p.RequestWith(ctx, broker)
	if err != nil {
		return errors.New("Produce ACL request failed at transport/protocol level; denial unproven")
	}
	if pr == nil || len(pr.Topics) != 1 || len(pr.Topics[0].Partitions) != 1 ||
		pr.Topics[0].Topic != "lab.jobs.v1" || pr.Topics[0].Partitions[0].Partition != -1 {
		return errors.New("unexpected Produce ACL response shape")
	}
	code := pr.Topics[0].Partitions[0].ErrorCode
	if code != kerr.TopicAuthorizationFailed.Code {
		return fmt.Errorf("Produce denial unproven: expected TOPIC_AUTHORIZATION_FAILED, got code %d", code)
	}
	if err := emit("produce_denied", code); err != nil {
		return err
	}
	or, err := o.RequestWith(ctx, broker)
	if err != nil {
		return errors.New("OffsetCommit ACL request failed at transport/protocol level; denial unproven")
	}
	if or == nil || len(or.Topics) != 1 || len(or.Topics[0].Partitions) != 1 ||
		or.Topics[0].Topic != "lab.jobs.v1" || or.Topics[0].Partitions[0].Partition != -1 {
		return errors.New("unexpected OffsetCommit ACL response shape")
	}
	code = or.Topics[0].Partitions[0].ErrorCode
	if code != kerr.GroupAuthorizationFailed.Code {
		return fmt.Errorf("OffsetCommit denial unproven: expected GROUP_AUTHORIZATION_FAILED, got code %d", code)
	}
	return emit("offset_commit_denied", code)
}
