package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
	"github.com/Spudifull/kubernetes-application-lab/internal/kafka"
	"github.com/Spudifull/kubernetes-application-lab/internal/tracing"
	"github.com/Spudifull/kubernetes-application-lab/internal/worker"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/kversion"
)

func kafkaEnvironment() kafka.Config {
	return kafka.Config{Bootstrap: os.Getenv("LAB_KAFKA_BOOTSTRAP"), CAFile: os.Getenv("LAB_KAFKA_CA_FILE"), CertFile: os.Getenv("LAB_KAFKA_CERT_FILE"), KeyFile: os.Getenv("LAB_KAFKA_KEY_FILE")}
}
func diagnosticEvent(event string, v map[string]any) error {
	v["event"] = event
	v["observed_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	return json.NewEncoder(os.Stdout).Encode(v)
}

// A member can be visible before SyncGroup supplies its assignment. Missing
// data during a rebalance is unknown, not an empty assignment or corruption.
// Keep rejecting malformed nonempty data and missing data in a stable group.
func diagnosticAssignment(state string, data []byte) (map[string][]int32, bool, error) {
	if len(data) == 0 && (state == "PreparingRebalance" || state == "CompletingRebalance") {
		return nil, true, nil
	}
	assignment, err := kgo.ParseConsumerSyncAssignment(data)
	return assignment, false, err
}

// groupStatus never joins a consumer group and never writes/resets offsets.
// The diagnostic client caps OffsetFetch at v9 (topic names, not Kafka 4.2's
// v10 topic IDs); the actual consumer keeps the normal negotiated versions.
func groupStatus() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	versions := kversion.V3_9_0()
	versions.SetMaxKeyVersion(9, 9)
	c, err := kafka.NewClient(kafkaEnvironment(), kgo.MaxVersions(versions))
	if err != nil {
		return errors.New("diagnostic Kafka TLS configuration invalid")
	}
	defer c.Close()
	describe := kmsg.NewPtrDescribeGroupsRequest()
	describe.Groups = []string{worker.Group}
	groups, err := describe.RequestWith(ctx, c)
	if err != nil {
		return errors.New("group describe failed")
	}
	if len(groups.Groups) != 1 {
		return errors.New("expected one described group")
	}
	g := groups.Groups[0]
	if g.ErrorCode != 0 {
		return fmt.Errorf("group describe error_code=%d", g.ErrorCode)
	}
	if err := diagnosticEvent("worker_group_state", map[string]any{"group": g.Group, "state": g.State, "members": len(g.Members), "protocol": g.Protocol}); err != nil {
		return err
	}
	for _, m := range g.Members {
		assignment, pending, err := diagnosticAssignment(g.State, m.MemberAssignment)
		if err != nil {
			return errors.New("invalid group assignment")
		}
		if err := diagnosticEvent("worker_group_member", map[string]any{"group": g.Group, "group_state": g.State, "member_id": m.MemberID, "client_id": m.ClientID, "client_host": m.ClientHost, "assignment": assignment, "assignment_pending": pending}); err != nil {
			return err
		}
	}
	partitions := []int32{0, 1, 2} // Fixed lab.jobs.v1 topology, not a general Kafka admin tool.
	offsets := kmsg.NewPtrOffsetFetchRequest()
	group := kmsg.NewOffsetFetchRequestGroup()
	group.Group = worker.Group
	group.Topics = []kmsg.OffsetFetchRequestGroupTopic{{Topic: jobs.Topic, Partitions: partitions}}
	offsets.Groups = []kmsg.OffsetFetchRequestGroup{group}
	fetched, err := offsets.RequestWith(ctx, c)
	if err != nil {
		return errors.New("group offset fetch failed")
	}
	if len(fetched.Groups) != 1 || fetched.Groups[0].ErrorCode != 0 {
		return errors.New("group offset response failed")
	}
	ends := kmsg.NewPtrListOffsetsRequest()
	topic := kmsg.NewListOffsetsRequestTopic()
	topic.Topic = jobs.Topic
	for _, p := range partitions {
		part := kmsg.NewListOffsetsRequestTopicPartition()
		part.Partition = p
		part.Timestamp = -1
		topic.Partitions = append(topic.Partitions, part)
	}
	ends.Topics = []kmsg.ListOffsetsRequestTopic{topic}
	endResponse, err := ends.RequestWith(ctx, c)
	if err != nil {
		return errors.New("log end offset fetch failed")
	}
	endByPartition := map[int32]int64{}
	for _, t := range endResponse.Topics {
		for _, p := range t.Partitions {
			if p.ErrorCode != 0 {
				return fmt.Errorf("end offset error_code=%d", p.ErrorCode)
			}
			endByPartition[p.Partition] = p.Offset
		}
	}
	seen := 0
	for _, t := range fetched.Groups[0].Topics {
		for _, p := range t.Partitions {
			if p.ErrorCode != 0 {
				return fmt.Errorf("committed offset error_code=%d", p.ErrorCode)
			}
			end, ok := endByPartition[p.Partition]
			if !ok {
				return errors.New("missing end offset")
			}
			// A never-committed partition has no measured lag, not a synthetic zero.
			var lag any
			if p.Offset >= 0 && end >= p.Offset {
				lag = end - p.Offset
			}
			if err := diagnosticEvent("worker_group_offset", map[string]any{"group": worker.Group, "topic": jobs.Topic, "partition": p.Partition, "committed_offset": p.Offset, "log_end_offset": end, "lag": lag}); err != nil {
				return err
			}
			seen++
		}
	}
	if seen != 3 {
		return errors.New("expected three partition offset observations")
	}
	return nil
}

// replayEvent deliberately republishes one already-published outbox event.
// The diagnostic Pod must mount publisher credentials, not worker credentials.
func replayEvent(db *sql.DB, id string) error {
	if !jobs.ValidID(id) {
		return errors.New("replay requires a job UUID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	read, cancelRead := context.WithTimeout(ctx, 2*time.Second)
	var body []byte
	var parent string
	err := db.QueryRowContext(read, `SELECT payload,trace_parent FROM public.lab_outbox WHERE job_id=$1 AND published_at IS NOT NULL`, id).Scan(&body, &parent)
	cancelRead()
	if err != nil {
		return errors.New("published outbox event not available")
	}
	event, err := jobs.DecodeEvent([]byte(id), body)
	if err != nil {
		return err
	}
	c, err := kafka.NewClient(kafkaEnvironment())
	if err != nil {
		return errors.New("replay Kafka TLS configuration invalid")
	}
	defer c.Close()
	ctx = tracing.Extract(ctx, parent)
	r := &kgo.Record{Topic: jobs.Topic, Key: []byte(id), Value: body, Headers: tracing.KafkaHeaders(ctx)}
	if c.ProduceSync(ctx, r).FirstErr() != nil {
		return errors.New("replay publish failed")
	}
	return diagnosticEvent("worker_event_republished", map[string]any{"job_id": id, "event_id": event.EventID, "topic": r.Topic, "partition": r.Partition, "offset": r.Offset})
}
