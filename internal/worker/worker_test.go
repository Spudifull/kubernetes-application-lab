package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
	"github.com/twmb/franz-go/pkg/kgo"
)

type testStore struct {
	calls                 *[]string
	startErr, completeErr error
	duplicate             bool
	afterComplete         func()
	afterStart            func()
}

func (s testStore) Start(context.Context, jobs.Event) (bool, error) {
	*s.calls = append(*s.calls, "start")
	if s.afterStart != nil {
		s.afterStart()
	}
	return s.duplicate, s.startErr
}
func (s testStore) Complete(context.Context, jobs.Event) (bool, error) {
	*s.calls = append(*s.calls, "effect")
	if s.afterComplete != nil {
		s.afterComplete()
	}
	return false, s.completeErr
}

type testCommitter struct {
	calls *[]string
	err   error
}

func (c testCommitter) CommitRecords(context.Context, ...*kgo.Record) error {
	*c.calls = append(*c.calls, "offset")
	return c.err
}
func testRecord() *kgo.Record {
	e := jobs.Event{SchemaVersion: 1, EventID: "00000000-0000-4000-8000-000000000001", JobID: "00000000-0000-4000-8000-000000000002", JobType: "lab_effect", CreatedAt: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC), Payload: jobs.Payload{DurationMS: 1000}}
	b, _ := json.Marshal(e)
	return &kgo.Record{Topic: jobs.Topic, Key: []byte(e.JobID), Value: b, Partition: 0, Offset: 5}
}
func TestEffectBeforeOffset(t *testing.T) {
	failure := errors.New("failure")
	for _, tt := range []struct {
		name                          string
		start, work, complete, commit error
		duplicate                     bool
		want                          []string
		wantErr                       bool
	}{
		{"success", nil, nil, nil, nil, false, []string{"start", "work", "effect", "offset"}, false},
		{"duplicate", nil, nil, nil, nil, true, []string{"start", "offset"}, false},
		{"start failure", failure, nil, nil, nil, false, []string{"start"}, true},
		{"work failure", nil, failure, nil, nil, false, []string{"start", "work"}, true},
		{"SQL failure", nil, nil, failure, nil, false, []string{"start", "work", "effect"}, true},
		{"offset failure", nil, nil, nil, failure, false, []string{"start", "work", "effect", "offset"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := []string{}
			p := Processor{Store: testStore{calls: &calls, startErr: tt.start, completeErr: tt.complete, duplicate: tt.duplicate}, Committer: testCommitter{&calls, tt.commit}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Work: func(context.Context, time.Duration) error { calls = append(calls, "work"); return tt.work }}
			err := p.Handle(context.Background(), testRecord())
			if (err != nil) != tt.wantErr || !reflect.DeepEqual(calls, tt.want) {
				t.Fatalf("calls=%v err=%v", calls, err)
			}
		})
	}
}
func TestCancelAfterEffectDoesNotCommitOffset(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := []string{}
	p := Processor{Store: testStore{calls: &calls, afterComplete: cancel}, Committer: testCommitter{calls: &calls}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Work: func(context.Context, time.Duration) error { return nil }}
	if err := p.Handle(ctx, testRecord()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"start", "effect"}) {
		t.Fatal(calls)
	}
}
func TestInvalidEventHasNoEffectOrCommit(t *testing.T) {
	r := testRecord()
	r.Value = []byte(`{}`)
	p := Processor{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := p.Handle(context.Background(), r); !errors.Is(err, jobs.ErrEvent) {
		t.Fatal(err)
	}
}
func TestGateRemembersEarlyRebalanceAndCancelsCurrentAttempt(t *testing.T) {
	var g Gate
	g.Block()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g.bind(cancel)
	if ctx.Err() == nil {
		t.Fatal("early rebalance signal lost")
	}
	g.unbind()
	g.change(map[string][]int32{jobs.Topic: {0}}, true)
	if !g.Ready() {
		t.Fatal("assignment not ready")
	}
	next, stop := context.WithCancel(context.Background())
	defer stop()
	g.bind(stop)
	g.Block()
	if next.Err() == nil || g.Ready() {
		t.Fatal("current work not canceled")
	}
	g.unbind()
	g.change(map[string][]int32{jobs.Topic: {0}}, false)
	if g.Ready() {
		t.Fatal("revoked assignment still ready")
	}
}
func TestGroupOptionsAreValid(t *testing.T) {
	opts := append([]kgo.Opt{kgo.SeedBrokers("broker:9093")}, Options(&Gate{}, slog.New(slog.NewTextHandler(io.Discard, nil)))...)
	if err := kgo.ValidateOpts(opts...); err != nil {
		t.Fatal(err)
	}
}

type testConsumer struct {
	testCommitter
	polls, allowed int
	record         *kgo.Record
}

func (c *testConsumer) PollRecords(context.Context, int) kgo.Fetches {
	c.polls++
	r := c.record
	if r == nil {
		r = testRecord()
	}
	return kgo.Fetches{{Topics: []kgo.FetchTopic{{Topic: jobs.Topic, Partitions: []kgo.FetchPartition{{Partition: 0, Records: []*kgo.Record{r}}}}}}}
}
func (c *testConsumer) AllowRebalance() { c.allowed++ }
func TestProcessingErrorStopsPollingBeforeLaterOffset(t *testing.T) {
	calls := []string{}
	c := &testConsumer{testCommitter: testCommitter{calls: &calls}}
	err := Consume(context.Background(), c, testStore{calls: &calls, startErr: errors.New("DB")}, &Gate{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || c.polls != 1 || c.allowed != 1 || !reflect.DeepEqual(calls, []string{"start"}) {
		t.Fatalf("polls=%d allowed=%d calls=%v err=%v", c.polls, c.allowed, calls, err)
	}
}

func TestStopPollingLetsAcceptedAttemptComplete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := testRecord()
	e, err := jobs.DecodeEvent(r.Key, r.Value)
	if err != nil {
		t.Fatal(err)
	}
	e.Payload.DurationMS = 1
	r.Value, err = json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	c := &testConsumer{testCommitter: testCommitter{calls: &calls}, record: r}
	err = Consume(ctx, c, testStore{calls: &calls, afterStart: cancel}, &Gate{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil || c.polls != 1 || c.allowed != 1 || !reflect.DeepEqual(calls, []string{"start", "effect", "offset"}) {
		t.Fatalf("polls=%d allowed=%d calls=%v err=%v", c.polls, c.allowed, calls, err)
	}
}
