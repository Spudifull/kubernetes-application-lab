package publisher

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Spudifull/kubernetes-application-lab/internal/telemetry"
)

func TestMetricsDistinguishAckFromDurableOutboxMark(t *testing.T) {
	for _, tc := range []struct {
		name                string
		kafkaErr, dbErr     error
		ack, marked, failed string
	}{
		{"success", nil, nil, "1", "1", "0"},
		{"publish failed", errors.New("private-kafka-error"), nil, "0", "0", "1"},
		{"mark failed after ack", nil, errors.New("private-sql-error"), "1", "0", "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := []string{}
			m := telemetry.NewPublisher(telemetry.Build{})
			p := Publisher{Store: fakeStore{&calls, tc.dbErr}, Producer: fakeProducer{&calls, tc.kafkaErr}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Metrics: m}
			_ = p.Step(context.Background())
			w := httptest.NewRecorder()
			m.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
			text := w.Body.String()
			for _, want := range []string{"lab_publisher_publish_success_total " + tc.ack + "\n", "lab_publisher_outbox_published_total " + tc.marked + "\n", "lab_publisher_publish_errors_total " + tc.failed + "\n", "lab_publisher_attempts_total 1\n", "lab_publisher_active 0\n"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q: %s", want, text)
				}
			}
			if strings.Contains(text, "private-") {
				t.Fatal("error leaked")
			}
		})
	}
}
