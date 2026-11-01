package main

import (
	"context"
	"errors"
	"testing"

	"github.com/twmb/franz-go/pkg/kmsg"
)

type aclRequestor struct {
	t               *testing.T
	produce, commit int16
	transport       bool
	calls           int
}

func (f *aclRequestor) Request(_ context.Context, req kmsg.Request) (kmsg.Response, error) {
	f.calls++
	if f.transport {
		return nil, errors.New("connection refused")
	}
	switch r := req.(type) {
	case *kmsg.ProduceRequest:
		if len(r.Topics) != 1 || r.Topics[0].Topic != "lab.jobs.v1" || len(r.Topics[0].Partitions) != 1 {
			f.t.Fatal("unexpected Produce target")
		}
		p := r.Topics[0].Partitions[0]
		if p.Partition != -1 || len(p.Records) != 0 || r.TransactionID != nil {
			f.t.Fatal("probe may write a record or target a real partition")
		}
		return &kmsg.ProduceResponse{Topics: []kmsg.ProduceResponseTopic{{Topic: "lab.jobs.v1", Partitions: []kmsg.ProduceResponseTopicPartition{{Partition: -1, ErrorCode: f.produce}}}}}, nil
	case *kmsg.OffsetCommitRequest:
		if r.Group != "lab-workers" || r.MemberID != "" || r.Generation != -1 || len(r.Topics) != 1 ||
			r.Topics[0].Topic != "lab.jobs.v1" || len(r.Topics[0].Partitions) != 1 {
			f.t.Fatal("unexpected OffsetCommit target or member")
		}
		p := r.Topics[0].Partitions[0]
		if p.Partition != -1 || p.Offset != -1 {
			f.t.Fatal("probe may change a real partition offset")
		}
		return &kmsg.OffsetCommitResponse{Topics: []kmsg.OffsetCommitResponseTopic{{Topic: "lab.jobs.v1", Partitions: []kmsg.OffsetCommitResponseTopicPartition{{Partition: -1, ErrorCode: f.commit}}}}}, nil
	default:
		f.t.Fatalf("unexpected request %T", req)
		return nil, nil
	}
}

func TestMonitoringACLRequiresExactDenials(t *testing.T) {
	for _, tc := range []struct {
		name            string
		produce, commit int16
		transport, pass bool
	}{
		{"denied", 29, 30, false, true},
		{"produce_allowed", 0, 30, false, false},
		{"invalid_partition_is_not_denial", 3, 30, false, false},
		{"commit_allowed", 29, 0, false, false},
		{"wrong_commit_error", 29, 29, false, false},
		{"network_failure", 29, 30, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &aclRequestor{t: t, produce: tc.produce, commit: tc.commit, transport: tc.transport}
			var probes []string
			err := runMonitoringACL(context.Background(), f, func(name string, _ int16) error {
				probes = append(probes, name)
				return nil
			})
			if (err == nil) != tc.pass {
				t.Fatalf("err=%v pass=%v", err, tc.pass)
			}
			if tc.pass && (f.calls != 2 || len(probes) != 2) {
				t.Fatal("both denials must be recorded")
			}
			if (tc.transport || tc.produce != 29) && (f.calls != 1 || len(probes) != 0) {
				t.Fatal("failed first probe must stop without reporting success")
			}
		})
	}
}
