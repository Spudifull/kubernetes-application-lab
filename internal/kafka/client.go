// Package kafka owns the laboratory's bounded, authenticated Kafka transport.
// Database effects and offset-commit decisions belong to their callers.
package kafka

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

type Config struct {
	Bootstrap string
	CAFile    string
	CertFile  string
	KeyFile   string
}

func (c Config) TLS() (*tls.Config, error) {
	if c.Bootstrap == "" || c.CAFile == "" || c.CertFile == "" || c.KeyFile == "" {
		return nil, errors.New("Kafka bootstrap, CA and client certificate/key paths are required")
	}
	ca, err := os.ReadFile(c.CAFile)
	if err != nil {
		return nil, fmt.Errorf("read Kafka CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("Kafka CA contains no usable certificate")
	}
	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load Kafka client certificate: %w", err)
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{cert}}, nil
}

// NewClient creates a producer or direct consumer by default. Group consumers
// must supply ConsumerGroup and DisableAutoCommit together. Autocommit options
// are invalid without a group; direct consumers have no group offsets to commit.
// Producers retain franz-go's default idempotence and all-ISR acknowledgements.
// Callers must supply contexts and close the returned client.
func NewClient(c Config, options ...kgo.Opt) (*kgo.Client, error) {
	tlsConfig, err := c.TLS()
	if err != nil {
		return nil, err
	}
	return kgo.NewClient(clientOptions(c.Bootstrap, tlsConfig, options...)...)
}

// Keep the exact constructor options available to network-free validation tests.
func clientOptions(bootstrap string, tlsConfig *tls.Config, options ...kgo.Opt) []kgo.Opt {
	opts := []kgo.Opt{
		kgo.SeedBrokers(bootstrap), kgo.DialTLSConfig(tlsConfig),
		kgo.DialTimeout(5 * time.Second), kgo.RequestTimeoutOverhead(5 * time.Second),
		kgo.ProduceRequestTimeout(10 * time.Second), kgo.RecordDeliveryTimeout(20 * time.Second),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.MaxBufferedRecords(32), kgo.ProducerBatchMaxBytes(65536),
		kgo.FetchMaxBytes(1048576), kgo.FetchMaxPartitionBytes(1048576),
		kgo.MaxDecompressBatchBytes(1048576),
	}
	return append(opts, options...)
}
