package publisher

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
	"github.com/Spudifull/kubernetes-application-lab/internal/tracing"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type tracedClaim struct{ fakeStore }

func (s tracedClaim) Claim(ctx context.Context) (jobs.Claim, error) {
	c, err := s.fakeStore.Claim(ctx)
	c.TraceParent = "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"
	return c, err
}

type tracedProducer struct {
	fakeProducer
	t *testing.T
}

func (p tracedProducer) ProduceSync(ctx context.Context, records ...*kgo.Record) kgo.ProduceResults {
	if len(records[0].Headers) != 1 || string(records[0].Headers[0].Value) != tracing.Parent(ctx) || trace.SpanContextFromContext(ctx).TraceID().String() != "0123456789abcdef0123456789abcdef" {
		p.t.Fatal("outbox context not forwarded")
	}
	return p.fakeProducer.ProduceSync(ctx, records...)
}
func TestPersistedParentReachesKafka(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous); _ = provider.Shutdown(context.Background()) })
	calls := []string{}
	p := Publisher{Store: tracedClaim{fakeStore{calls: &calls}}, Producer: tracedProducer{fakeProducer{calls: &calls}, t}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := p.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	spans := recorder.Ended()
	if len(spans) != 3 {
		t.Fatalf("span count %d", len(spans))
	}
	for _, s := range spans {
		if s.StartTime().Before(spans[len(spans)-1].StartTime()) {
			t.Fatal("claim precedes attempt parent")
		}
	}
	if spans[len(spans)-1].Parent().SpanID().String() != "0123456789abcdef" {
		t.Fatal("persisted parent lost")
	}
}
