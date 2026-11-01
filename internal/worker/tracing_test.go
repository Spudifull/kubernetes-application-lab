package worker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestKafkaParentReachesWorkerAndOffset(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous); _ = provider.Shutdown(context.Background()) })
	calls := []string{}
	r := testRecord()
	r.Headers = []kgo.RecordHeader{{Key: "traceparent", Value: []byte("00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")}}
	p := Processor{Store: testStore{calls: &calls}, Committer: testCommitter{calls: &calls}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Work: func(ctx context.Context, _ time.Duration) error {
		if trace.SpanContextFromContext(ctx).TraceID().String() != "0123456789abcdef0123456789abcdef" {
			t.Fatal("Kafka parent lost")
		}
		return nil
	}}
	if err := p.Handle(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	spans := recorder.Ended()
	if len(spans) != 3 || spans[2].Parent().SpanID().String() != "0123456789abcdef" || spans[1].Name() != "lab-workers commit" {
		t.Fatal("consumer/offset span graph lost")
	}
}
