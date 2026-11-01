// kafka-probe is an explicitly assigned, one-record TLS transport check.
// It is not a worker, does not join lab-workers, and never commits group offsets.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"time"

	labkafka "github.com/Spudifull/kubernetes-application-lab/internal/kafka"
	"github.com/twmb/franz-go/pkg/kgo"
)

const smokeTopic = "lab.smoke.v1"

var runKey = regexp.MustCompile(`^smoke-[a-z0-9-]{1,100}$`)

type locator struct {
	Event     string `json:"event"`
	Key       string `json:"key"`
	Topic     string `json:"topic"`
	Partition int32  `json:"partition"`
	Offset    int64  `json:"offset"`
	Time      string `json:"observed_at"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "kafka-probe:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("kafka-probe", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	mode := f.String("mode", "roundtrip", "roundtrip, write, or read")
	key := f.String("key", "", "unique smoke correlation key")
	partition := f.Int("partition", -1, "previously acknowledged partition")
	offset := f.Int64("offset", -1, "previously acknowledged offset")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || !runKey.MatchString(*key) || (*mode != "roundtrip" && *mode != "write" && *mode != "read") {
		return errors.New("invalid mode/key or unexpected arguments")
	}
	if *mode == "read" && (*partition != 0 || *offset < 0) {
		return errors.New("read requires smoke partition 0 and nonnegative acknowledged offset")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cfg := labkafka.Config{
		Bootstrap: "lab-kafka-kafka-bootstrap.kubernetes-application-lab-kafka.svc.cluster.local:9093",
		CAFile:    "/tls/ca/ca.crt", CertFile: "/tls/user/user.crt", KeyFile: "/tls/user/user.key",
	}
	pos := locator{Key: *key, Topic: smokeTopic, Partition: int32(*partition), Offset: *offset}
	if *mode != "read" {
		cl, err := labkafka.NewClient(cfg)
		if err != nil {
			return err
		}
		// The value is diagnostic text on a separate topic, never a job event.
		record := &kgo.Record{Topic: smokeTopic, Key: []byte(*key), Value: []byte(*key)}
		err = cl.ProduceSync(ctx, record).FirstErr()
		cl.Close()
		if err != nil {
			return fmt.Errorf("publish smoke and await broker ack: %w", err)
		}
		pos.Partition, pos.Offset = record.Partition, record.Offset
		pos.Event, pos.Time = "kafka_smoke_written", time.Now().UTC().Format(time.RFC3339Nano)
		if err := json.NewEncoder(out).Encode(pos); err != nil {
			return err
		}
		if *mode == "write" {
			return nil
		}
	}
	cl, err := labkafka.NewClient(cfg, kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{
		smokeTopic: {pos.Partition: kgo.NewOffset().At(pos.Offset)},
	}))
	if err != nil {
		return err
	}
	defer cl.Close()
	for ctx.Err() == nil {
		fetches := cl.PollRecords(ctx, 1)
		if errs := fetches.Errors(); len(errs) != 0 {
			return fmt.Errorf("fetch smoke: %w", errs[0].Err)
		}
		for _, record := range fetches.Records() {
			if record.Topic != smokeTopic || record.Partition != pos.Partition || record.Offset != pos.Offset || string(record.Key) != pos.Key || string(record.Value) != pos.Key {
				return errors.New("acknowledged smoke record was not found at its original offset; possible retention/data loss or wrong locator")
			}
			pos.Event, pos.Time = "kafka_smoke_read", time.Now().UTC().Format(time.RFC3339Nano)
			return json.NewEncoder(out).Encode(pos)
		}
	}
	return ctx.Err()
}
