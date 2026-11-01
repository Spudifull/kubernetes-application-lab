package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
	"github.com/Spudifull/kubernetes-application-lab/internal/tracing"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type dlqPublisher struct{ client *kgo.Client }

func (p dlqPublisher) Publish(ctx context.Context, d jobs.DeadLetter) (partition int32, offset int64, publishErr error) {
	ctx, span := tracing.Start(ctx, "lab.jobs.dlq.v1 publish", trace.SpanKindProducer, attribute.String("messaging.system", "kafka"), attribute.String("messaging.destination.name", jobs.DLQTopic))
	defer func() { tracing.End(span, publishErr, "dlq_publish_failed") }()
	body, err := json.Marshal(d)
	if err != nil {
		return 0, 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	r := &kgo.Record{Topic: jobs.DLQTopic, Key: []byte(d.Key()), Value: body, Headers: tracing.KafkaHeaders(ctx)}
	if err := p.client.ProduceSync(ctx, r).FirstErr(); err != nil {
		return 0, 0, err
	}
	return r.Partition, r.Offset, nil
}
