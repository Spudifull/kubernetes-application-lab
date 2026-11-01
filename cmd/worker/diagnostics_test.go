package main

import (
	"reflect"
	"testing"

	"github.com/twmb/franz-go/pkg/kmsg"
)

func TestDiagnosticAssignmentDuringRebalance(t *testing.T) {
	assigned := kmsg.NewConsumerMemberAssignment()
	assigned.Topics = []kmsg.ConsumerMemberAssignmentTopic{{Topic: "lab.jobs.v1", Partitions: []int32{0, 2}}}
	empty := kmsg.NewConsumerMemberAssignment()
	for _, tt := range []struct {
		name, state string
		data        []byte
		want        map[string][]int32
		pending     bool
		wantErr     bool
	}{
		{"joining member", "PreparingRebalance", nil, nil, true, false},
		{"sync pending", "CompletingRebalance", []byte{}, nil, true, false},
		{"stable missing", "Stable", nil, nil, false, true},
		{"unknown state missing", "Unknown", nil, nil, false, true},
		{"corrupt during rebalance", "PreparingRebalance", []byte{0}, nil, false, true},
		{"stable assigned", "Stable", assigned.AppendTo(nil), map[string][]int32{"lab.jobs.v1": {0, 2}}, false, false},
		{"old assignment during rebalance", "PreparingRebalance", assigned.AppendTo(nil), map[string][]int32{"lab.jobs.v1": {0, 2}}, false, false},
		{"valid empty assignment", "Stable", empty.AppendTo(nil), map[string][]int32{}, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, pending, err := diagnosticAssignment(tt.state, tt.data)
			if (err != nil) != tt.wantErr || pending != tt.pending || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("assignment=%v pending=%v err=%v", got, pending, err)
			}
		})
	}
}
