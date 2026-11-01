package publisher

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
	"github.com/twmb/franz-go/pkg/kgo"
)

type fakeStore struct {
	calls     *[]string
	markError error
}

func (s fakeStore) Claim(context.Context) (jobs.Claim, error) {
	*s.calls = append(*s.calls, "claim")
	return jobs.Claim{EventID: "event", JobID: "job", Topic: jobs.Topic, Token: "token", Attempt: 1, Payload: []byte(`{}`)}, nil
}

type blockingProducer struct {
	entered chan context.Context
	release chan struct{}
}

func (p blockingProducer) ProduceSync(ctx context.Context, records ...*kgo.Record) kgo.ProduceResults {
	p.entered <- ctx
	<-p.release
	records[0].Partition = 2
	records[0].Offset = 7
	return kgo.ProduceResults{{Record: records[0]}}
}

func TestShutdownFinishesAcceptedAttempt(t *testing.T) {
	calls := []string{}
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := Publisher{Store: fakeStore{calls: &calls}, Producer: blockingProducer{entered, release}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	var attempt context.Context
	select {
	case attempt = <-entered:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("attempt not started")
	}
	cancel()
	if attempt.Err() != nil {
		close(release)
		<-done
		t.Fatal("signal canceled accepted delivery")
	}
	if _, ok := attempt.Deadline(); !ok {
		close(release)
		<-done
		t.Fatal("attempt has no bound")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after accepted attempt")
	}
	if !reflect.DeepEqual(calls, []string{"claim", "mark"}) {
		t.Fatalf("new claim after stop: %v", calls)
	}
}
func (s fakeStore) Published(_ context.Context, c jobs.Claim, partition int32, offset int64) error {
	if c.Token != "token" || partition != 2 || offset != 7 {
		return errors.New("ack metadata lost")
	}
	*s.calls = append(*s.calls, "mark")
	return s.markError
}
func (s fakeStore) Retry(context.Context, jobs.Claim) error {
	*s.calls = append(*s.calls, "retry")
	return nil
}

type fakeProducer struct {
	calls *[]string
	err   error
}

func (p fakeProducer) ProduceSync(_ context.Context, records ...*kgo.Record) kgo.ProduceResults {
	*p.calls = append(*p.calls, "produce")
	r := records[0]
	if string(r.Key) != "job" || r.Topic != jobs.Topic {
		panic("record contract lost")
	}
	r.Partition = 2
	r.Offset = 7
	return kgo.ProduceResults{{Record: r, Err: p.err}}
}

func TestAckBeforeMarkAndRecoverableFailure(t *testing.T) {
	for _, tt := range []struct {
		name                string
		produceErr, markErr error
		calls               []string
		wantError           bool
	}{
		{"ack", nil, nil, []string{"claim", "produce", "mark"}, false},
		{"Kafka unavailable", errors.New("unavailable"), nil, []string{"claim", "produce", "retry"}, false},
		{"ack then DB failure", nil, errors.New("db"), []string{"claim", "produce", "mark"}, true},
		{"expired token", nil, jobs.ErrLeaseLost, []string{"claim", "produce", "mark"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := []string{}
			p := Publisher{Store: fakeStore{&calls, tt.markErr}, Producer: fakeProducer{&calls, tt.produceErr}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			err := p.Step(context.Background())
			if (err != nil) != tt.wantError || !reflect.DeepEqual(calls, tt.calls) {
				t.Fatalf("calls=%v err=%v", calls, err)
			}
		})
	}
}
