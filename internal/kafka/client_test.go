package kafka

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
)

func TestTLSRequiresExplicitTrustAndClientCredentials(t *testing.T) {
	for _, cfg := range []Config{
		{}, {Bootstrap: "broker:9093"},
		{Bootstrap: "broker:9093", CAFile: "ca", CertFile: "cert"},
	} {
		if _, err := cfg.TLS(); err == nil {
			t.Fatal("incomplete configuration accepted")
		}
	}
}

func TestClientOptionsSupportProducerDirectConsumerAndManualGroup(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []kgo.Opt
	}{
		{name: "producer_without_group"},
		{name: "direct_consumer_without_group", opts: []kgo.Opt{
			kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{
				"lab.smoke.v1": {0: kgo.NewOffset().At(0)},
			}),
		}},
		{name: "group_consumer_manual_commit", opts: []kgo.Opt{
			kgo.ConsumerGroup("lab-workers"), kgo.DisableAutoCommit(), kgo.ConsumeTopics("lab.jobs.v1"),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// ValidateOpts uses the same validation as NewClient without opening
			// sockets or starting background consumer/producer goroutines.
			opts := clientOptions("broker:9093", &tls.Config{MinVersion: tls.VersionTLS12}, tc.opts...)
			if err := kgo.ValidateOpts(opts...); err != nil {
				t.Fatalf("invalid client configuration: %v", err)
			}
		})
	}
}

func TestGroupAutocommitOptionIsNotValidForProducer(t *testing.T) {
	opts := clientOptions("broker:9093", &tls.Config{MinVersion: tls.VersionTLS12}, kgo.DisableAutoCommit())
	if err := kgo.ValidateOpts(opts...); err == nil {
		t.Fatal("expected franz-go to reject autocommit configuration without a group")
	}
}

func TestTLSRejectsInvalidCA(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(path, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Config{Bootstrap: "broker:9093", CAFile: path, CertFile: "cert", KeyFile: "key"}).TLS(); err == nil {
		t.Fatal("invalid CA accepted")
	}
}
