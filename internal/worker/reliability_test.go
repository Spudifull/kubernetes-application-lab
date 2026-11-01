package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
)

type reliabilityTestStore struct {
	calls                  *[]string
	state                  jobs.RetryState
	published              bool
	prepareErr, persistErr error
	last                   jobs.DeadLetter
}

func (s *reliabilityTestStore) RetryState(context.Context, jobs.Event) (jobs.RetryState, error) {
	*s.calls = append(*s.calls, "state")
	return s.state, nil
}
func (s *reliabilityTestStore) RecordFailure(context.Context, jobs.Event, int) error {
	*s.calls = append(*s.calls, "failure")
	s.state.Failures++
	return nil
}
func (s *reliabilityTestStore) PrepareDeadLetter(_ context.Context, d jobs.DeadLetter) (bool, error) {
	*s.calls = append(*s.calls, "prepare")
	s.last = d
	return s.published, s.prepareErr
}
func (s *reliabilityTestStore) DeadLetterPublished(context.Context, jobs.DeadLetter, int32, int64) error {
	*s.calls = append(*s.calls, "receipt")
	if s.persistErr == nil {
		s.published = true
	}
	return s.persistErr
}

type reliabilityTestPublisher struct {
	calls *[]string
	err   error
}

func (p reliabilityTestPublisher) Publish(context.Context, jobs.DeadLetter) (int32, int64, error) {
	*p.calls = append(*p.calls, "publish")
	return 1, 4, p.err
}

func TestPersistedFailuresThenSuccess(t *testing.T) {
	calls := []string{}
	s := &reliabilityTestStore{calls: &calls, state: jobs.RetryState{FailuresBeforeSuccess: 2}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for attempt := range 3 {
		// Recreate Processor: retries depend on persisted state, not its lifetime.
		p := Processor{Store: testStore{calls: &calls}, Committer: testCommitter{calls: &calls}, Logger: logger,
			Work:        func(context.Context, time.Duration) error { calls = append(calls, "work"); return nil },
			Reliability: &Reliability{Store: s, Publisher: reliabilityTestPublisher{calls: &calls}}}
		err := p.Handle(context.Background(), testRecord())
		if attempt < 2 && !errors.Is(err, ErrRetryPending) {
			t.Fatalf("attempt=%d err=%v", attempt, err)
		}
		if attempt == 2 && err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"start", "state", "work", "failure", "start", "state", "work", "failure", "start", "state", "work", "effect", "offset"}
	if s.state.Failures != 2 || !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v failures=%d", calls, s.state.Failures)
	}
}

func TestDeadLetterOrderingAndFailures(t *testing.T) {
	failure := errors.New("unavailable")
	for _, tt := range []struct {
		name                              string
		prepare, publish, receipt, commit error
		published                         bool
		want                              []string
		wantErr                           bool
	}{
		{"success", nil, nil, nil, nil, false, []string{"prepare", "publish", "receipt", "offset"}, false},
		{"prepare fails", failure, nil, nil, nil, false, []string{"prepare"}, true},
		{"DLQ unavailable", nil, failure, nil, nil, false, []string{"prepare", "publish"}, true},
		{"ack before SQL failure", nil, nil, failure, nil, false, []string{"prepare", "publish", "receipt"}, true},
		{"source commit fails", nil, nil, nil, failure, false, []string{"prepare", "publish", "receipt", "offset"}, true},
		{"redelivery with receipt", nil, nil, nil, nil, true, []string{"prepare", "offset"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := []string{}
			s := &reliabilityTestStore{calls: &calls, prepareErr: tt.prepare, persistErr: tt.receipt, published: tt.published}
			p := Processor{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Committer: testCommitter{&calls, tt.commit},
				Reliability: &Reliability{Store: s, Publisher: reliabilityTestPublisher{&calls, tt.publish}}}
			r := testRecord()
			r.Value = []byte(`{"password":"do-not-copy-this"`)
			err := p.Handle(context.Background(), r)
			if (err != nil) != tt.wantErr || !reflect.DeepEqual(calls, tt.want) {
				t.Fatalf("calls=%v err=%v", calls, err)
			}
			b, _ := json.Marshal(s.last)
			if strings.Contains(string(b), "do-not-copy-this") || s.last.JobID != "" || s.last.SourceOffset != r.Offset || len(s.last.ValueSHA256) != 64 {
				t.Fatal("DLQ leaked body, trusted invalid job, or lost source identity")
			}
		})
	}
}

func TestExhaustedFailureAndRetryWait(t *testing.T) {
	for _, tt := range []struct {
		name    string
		state   jobs.RetryState
		want    []string
		wantErr bool
	}{
		{"exhausted", jobs.RetryState{Failures: 3, FailuresBeforeSuccess: 3}, []string{"start", "state", "prepare", "publish", "receipt", "offset"}, false},
		{"not due", jobs.RetryState{Failures: 1, Wait: time.Second}, []string{"start", "state"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := []string{}
			s := &reliabilityTestStore{calls: &calls, state: tt.state}
			p := Processor{Store: testStore{calls: &calls}, Committer: testCommitter{calls: &calls}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Reliability: &Reliability{Store: s, Publisher: reliabilityTestPublisher{calls: &calls}}}
			err := p.Handle(context.Background(), testRecord())
			if (err != nil) != tt.wantErr || !reflect.DeepEqual(calls, tt.want) {
				t.Fatalf("calls=%v err=%v", calls, err)
			}
			if !tt.wantErr && (s.last.Failures != 3 || !jobs.ValidID(s.last.JobID)) {
				t.Fatal("terminal source not correlated")
			}
		})
	}
}

func TestCanceledWorkDoesNotSpendControlledFailure(t *testing.T) {
	calls := []string{}
	s := &reliabilityTestStore{calls: &calls, state: jobs.RetryState{FailuresBeforeSuccess: 3}}
	p := Processor{Store: testStore{calls: &calls}, Committer: testCommitter{calls: &calls}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Work: func(context.Context, time.Duration) error { return context.Canceled }, Reliability: &Reliability{Store: s}}
	if err := p.Handle(context.Background(), testRecord()); !errors.Is(err, context.Canceled) || s.state.Failures != 0 || !reflect.DeepEqual(calls, []string{"start", "state"}) {
		t.Fatalf("calls=%v err=%v", calls, err)
	}
}
