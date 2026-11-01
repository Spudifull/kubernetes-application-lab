package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/jobs"
	"github.com/Spudifull/kubernetes-application-lab/internal/kafka"
	"github.com/twmb/franz-go/pkg/kgo"
)

func invalidRecord(mode string) (*kgo.Record, error) {
	const id = "00000000-0000-4000-8000-000000000099"
	e := jobs.Event{SchemaVersion: 1, JobID: id, EventID: "00000000-0000-4000-8000-000000000098",
		JobType: "lab_effect", CreatedAt: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC), Payload: jobs.Payload{DurationMS: 1}}
	switch mode {
	case "unknown-version":
		e.SchemaVersion = 2
	case "unknown-type":
		e.JobType = "unsupported"
	case "invalid-json", "missing-job", "oversized":
	default:
		return nil, errors.New("poison mode must be invalid-json, unknown-version, unknown-type, missing-job or oversized")
	}
	body, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	if mode == "invalid-json" {
		body = []byte(`{`)
	}
	if mode == "oversized" {
		body = []byte(strings.Repeat("x", 4097))
	}
	return &kgo.Record{Topic: jobs.Topic, Key: []byte(id), Value: body}, nil
}

func publishInvalid(mode string) error {
	r, err := invalidRecord(mode)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := kafka.NewClient(kafkaEnvironment())
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.ProduceSync(ctx, r).FirstErr(); err != nil {
		return errors.New("poison publication failed")
	}
	return diagnosticEvent("worker_invalid_published", map[string]any{"mode": mode, "topic": r.Topic, "partition": r.Partition, "offset": r.Offset, "bytes": len(r.Value)})
}

// The inspector reads exactly one acknowledged DLQ record without a group,
// commits, offset resets or permission to publish to either topic.
func readDeadLetter(db *sql.DB, id string) error {
	if id != "latest-invalid" && !jobs.ValidID(id) {
		return errors.New("DLQ read requires a job UUID or latest-invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	query := `SELECT envelope,dlq_partition,dlq_offset FROM public.lab_dead_letters
        WHERE published_at IS NOT NULL AND job_id=nullif($1,'')::uuid ORDER BY created_at DESC LIMIT 1`
	arg := id
	if id == "latest-invalid" {
		query = `SELECT envelope,dlq_partition,dlq_offset FROM public.lab_dead_letters
            WHERE published_at IS NOT NULL AND job_id IS NULL AND $1='' ORDER BY created_at DESC LIMIT 1`
		arg = ""
	}
	var body []byte
	var partition int32
	var offset int64
	if err := db.QueryRowContext(ctx, query, arg).Scan(&body, &partition, &offset); err != nil {
		return errors.New("acknowledged DLQ receipt not found")
	}
	var expected jobs.DeadLetter
	if json.Unmarshal(body, &expected) != nil {
		return errors.New("invalid stored DLQ envelope")
	}
	c, err := kafka.NewClient(kafkaEnvironment(), kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{
		jobs.DLQTopic: {partition: kgo.NewOffset().At(offset)},
	}))
	if err != nil {
		return err
	}
	defer c.Close()
	for ctx.Err() == nil {
		f := c.PollRecords(ctx, 1)
		if len(f.Errors()) > 0 {
			return errors.New("DLQ fetch failed")
		}
		for _, r := range f.Records() {
			var actual jobs.DeadLetter
			if r.Topic != jobs.DLQTopic || r.Partition != partition || r.Offset != offset || string(r.Key) != expected.Key() || json.Unmarshal(r.Value, &actual) != nil || actual != expected {
				return errors.New("DLQ record differs from persisted source and broker receipt")
			}
			return diagnosticEvent("worker_dlq_read", map[string]any{"topic": r.Topic, "partition": r.Partition, "offset": r.Offset, "envelope": actual})
		}
	}
	return ctx.Err()
}
