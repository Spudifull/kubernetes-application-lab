// Package tracing owns bounded, optional diagnostics, never business decisions.
package tracing

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type Config struct {
	Enabled     bool
	Endpoint    string
	SampleRatio float64
}

func ConfigFromEnv() (Config, error) {
	c := Config{Endpoint: os.Getenv("LAB_TRACING_ENDPOINT"), SampleRatio: 0.1}
	if value := os.Getenv("LAB_TRACING_ENABLED"); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return c, errors.New("invalid LAB_TRACING_ENABLED")
		}
		c.Enabled = enabled
	}
	if !c.Enabled {
		return c, nil
	}
	if value := os.Getenv("LAB_TRACING_SAMPLE_RATIO"); value != "" {
		ratio, err := strconv.ParseFloat(value, 64)
		if err != nil || !(ratio >= 0 && ratio <= 1) {
			return c, errors.New("invalid tracing sample ratio")
		}
		c.SampleRatio = ratio
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return c, errors.New("tracing requires an explicit internal HTTP Collector endpoint without credentials, path or query")
	}
	return c, nil
}

// Init does not dial the Collector. SDK enqueue is nonblocking and bounded;
// export failures cannot fail a job or alter readiness. Invalid local config is
// a startup error, while an unavailable telemetry backend is not.
func Init(service, version, revision string, logger *slog.Logger) (func(), error) {
	c, err := ConfigFromEnv()
	if err != nil {
		return nil, err
	}
	if !c.Enabled {
		return func() {}, nil
	}
	var lastError atomic.Int64
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {
		now := time.Now().Unix()
		previous := lastError.Load()
		if now-previous >= 30 && lastError.CompareAndSwap(previous, now) {
			logger.Warn("tracing_export_failed", "error_code", "telemetry_unavailable")
		}
	}))
	exporter, err := otlptracehttp.New(context.Background(), otlptracehttp.WithEndpointURL(c.Endpoint),
		otlptracehttp.WithTimeout(time.Second), otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: true,
			InitialInterval: 100 * time.Millisecond, MaxInterval: 500 * time.Millisecond, MaxElapsedTime: time.Second}))
	if err != nil {
		return nil, errors.New("cannot initialize tracing exporter")
	}
	limits := sdktrace.NewSpanLimits()
	limits.AttributeCountLimit, limits.AttributeValueLengthLimit = 32, 256
	limits.EventCountLimit, limits.LinkCountLimit = 16, 8
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(c.SampleRatio))),
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", service),
			attribute.String("service.version", version), attribute.String("lab.revision", revision),
			attribute.String("k8s.pod.name", os.Getenv("LAB_POD_NAME")))),
		sdktrace.WithSpanLimits(limits),
		sdktrace.WithBatcher(exporter, sdktrace.WithMaxQueueSize(128), sdktrace.WithMaxExportBatchSize(32),
			sdktrace.WithBatchTimeout(time.Second), sdktrace.WithExportTimeout(time.Second)))
	otel.SetTracerProvider(provider)
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if provider.Shutdown(ctx) != nil {
			logger.Warn("tracing_shutdown_incomplete", "error_code", "telemetry_unavailable")
		}
	}, nil
}

func StartAt(ctx context.Context, name string, start time.Time, kind trace.SpanKind, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer("lab").Start(ctx, name, trace.WithTimestamp(start), trace.WithSpanKind(kind), trace.WithAttributes(attrs...))
}

func Start(ctx context.Context, name string, kind trace.SpanKind, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer("lab").Start(ctx, name, trace.WithSpanKind(kind), trace.WithAttributes(attrs...))
}

// End emits only a fixed classification, never driver/broker errors or payload.
func End(span trace.Span, err error, code string) {
	if err != nil {
		span.SetStatus(codes.Error, code)
		span.SetAttributes(attribute.String("error.type", code))
	}
	span.End()
}

func Fields(ctx context.Context) []any {
	s := trace.SpanContextFromContext(ctx)
	if !s.IsValid() {
		return nil
	}
	return []any{"trace_id", s.TraceID().String(), "span_id", s.SpanID().String()}
}

// Only traceparent is persisted/forwarded. No baggage, tracestate, arbitrary
// HTTP headers or payload can cross this boundary. W3C parsing uses the SDK.
func Parent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return carrier.Get("traceparent")
}

func Extract(ctx context.Context, parent string) context.Context {
	if len(parent) != 55 {
		return ctx
	}
	return propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{"traceparent": parent})
}

func KafkaContext(ctx context.Context, headers []kgo.RecordHeader) context.Context {
	// Reject excessive diagnostic metadata rather than scanning unbounded headers.
	if len(headers) > 64 {
		return ctx
	}
	var parent string
	for _, h := range headers {
		if h.Key == "traceparent" {
			if parent != "" || len(h.Value) != 55 {
				return ctx
			}
			parent = string(h.Value)
		}
	}
	return Extract(ctx, parent)
}

func KafkaHeaders(ctx context.Context) []kgo.RecordHeader {
	if parent := Parent(ctx); parent != "" {
		return []kgo.RecordHeader{{Key: "traceparent", Value: []byte(parent)}}
	}
	return nil
}

// SQL instruments a named operation, never SQL text, parameters or row values.
func SQL[T any](ctx context.Context, name string, call func(context.Context) (T, error)) (T, error) {
	ctx, span := Start(ctx, name, trace.SpanKindClient, attribute.String("db.system.name", "postgresql"))
	value, err := call(ctx)
	End(span, err, "database_operation_failed")
	return value, err
}

type logHandler struct {
	slog.Handler
	service string
}

func Logger(base *slog.Logger, service string) *slog.Logger {
	return slog.New(logHandler{base.Handler(), service})
}
func (h logHandler) Handle(ctx context.Context, r slog.Record) error {
	r.Time = r.Time.UTC()
	r.AddAttrs(slog.String("service", h.service), slog.String("event", r.Message))
	for _, v := range []struct{ key, value string }{{"trace_id", trace.SpanContextFromContext(ctx).TraceID().String()}, {"span_id", trace.SpanContextFromContext(ctx).SpanID().String()}} {
		if trace.SpanContextFromContext(ctx).IsValid() {
			r.AddAttrs(slog.String(v.key, v.value))
		}
	}
	return h.Handler.Handle(ctx, r)
}
func (h logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return logHandler{h.Handler.WithAttrs(attrs), h.service}
}
func (h logHandler) WithGroup(name string) slog.Handler {
	return logHandler{h.Handler.WithGroup(name), h.service}
}
