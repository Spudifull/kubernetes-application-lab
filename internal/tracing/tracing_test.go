package tracing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const testParent = "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"

func TestPropagationBoundaries(t *testing.T) {
	ctx := Extract(context.Background(), testParent)
	if Parent(ctx) != testParent {
		t.Fatal("trace context lost")
	}
	for _, headers := range [][]kgo.RecordHeader{
		{{Key: "traceparent", Value: []byte(testParent)}, {Key: "baggage", Value: []byte("password=secret")}},
	} {
		out := KafkaHeaders(KafkaContext(context.Background(), headers))
		if len(out) != 1 || out[0].Key != "traceparent" || string(out[0].Value) != testParent {
			t.Fatal("unexpected forwarded headers", out)
		}
	}
	for _, headers := range [][]kgo.RecordHeader{
		{{Key: "traceparent", Value: []byte(testParent)}, {Key: "traceparent", Value: []byte(testParent)}},
		{{Key: "traceparent", Value: []byte("invalid")}},
		{{Key: "traceparent", Value: []byte("00-00000000000000000000000000000000-0123456789abcdef-01")}},
	} {
		if trace.SpanContextFromContext(KafkaContext(context.Background(), headers)).IsValid() {
			t.Fatal("invalid or ambiguous context accepted")
		}
	}
}

func TestConfigBounds(t *testing.T) {
	t.Setenv("LAB_TRACING_ENABLED", "true")
	t.Setenv("LAB_TRACING_ENDPOINT", "http://collector:4318")
	for _, ratio := range []string{"-1", "1.1", "NaN", "garbage"} {
		t.Setenv("LAB_TRACING_SAMPLE_RATIO", ratio)
		if _, err := ConfigFromEnv(); err == nil {
			t.Fatalf("accepted ratio %q", ratio)
		}
	}
	t.Setenv("LAB_TRACING_SAMPLE_RATIO", "1")
	for _, endpoint := range []string{"", "http://user:password@collector:4318", "http://collector:4318?token=secret", "http://collector:4318/v1/traces"} {
		t.Setenv("LAB_TRACING_ENDPOINT", endpoint)
		if _, err := ConfigFromEnv(); err == nil {
			t.Fatalf("accepted endpoint %q", endpoint)
		}
	}
	t.Setenv("LAB_TRACING_ENABLED", "false")
	if _, err := ConfigFromEnv(); err != nil {
		t.Fatal("disabled tracing requires exporter configuration", err)
	}
}

func TestSQLChildAndErrorPrivacy(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous); _ = provider.Shutdown(context.Background()) })
	ctx := Extract(context.Background(), testParent)
	_, err := SQL(ctx, "jobs.enqueue.transaction", func(context.Context) (bool, error) { return false, errors.New("password=secret SQL payload") })
	if err == nil {
		t.Fatal("business error swallowed")
	}
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Parent().SpanID().String() != "0123456789abcdef" || spans[0].Status().Code != codes.Error {
		t.Fatal("incorrect SQL span")
	}
	if spans[0].Status().Description != "database_operation_failed" || len(spans[0].Events()) != 0 {
		t.Fatal("raw error exported")
	}
}

func TestStructuredLogCorrelationAndUTC(t *testing.T) {
	var output bytes.Buffer
	logger := Logger(slog.New(slog.NewJSONHandler(&output, nil)), "lab-api").With("pod", "api-one")
	r := slog.NewRecord(time.Date(2026, 10, 31, 23, 0, 0, 0, time.FixedZone("test", 3*60*60)), slog.LevelInfo, "job_accepted", 0)
	if err := logger.Handler().Handle(Extract(context.Background(), testParent), r); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event["service"] != "lab-api" || event["event"] != "job_accepted" || event["pod"] != "api-one" || event["trace_id"] != "0123456789abcdef0123456789abcdef" || !strings.HasSuffix(event["time"].(string), "Z") {
		t.Fatal("log contract lost", event)
	}
}
