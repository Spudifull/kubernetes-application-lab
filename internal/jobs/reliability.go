package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/database"
	"github.com/Spudifull/kubernetes-application-lab/internal/tracing"
)

const DLQTopic = "lab.jobs.dlq.v1"
const MaxWorkerFailures = 3

var ErrJobFailed = errors.New("job already terminally failed")

type RetryState struct {
	Failures, FailuresBeforeSuccess int
	Wait                            time.Duration
}

// DeadLetter deliberately contains no original body or key: even invalid input
// may contain credentials. Hashes, sizes and a bounded reason support diagnosis.
type DeadLetter struct {
	Version         int    `json:"version"`
	SourceTopic     string `json:"source_topic"`
	SourcePartition int32  `json:"source_partition"`
	SourceOffset    int64  `json:"source_offset"`
	JobID           string `json:"job_id,omitempty"`
	EventID         string `json:"event_id,omitempty"`
	Reason          string `json:"reason"`
	Failures        int    `json:"failures"`
	ValueBytes      int    `json:"value_bytes"`
	ValueSHA256     string `json:"value_sha256"`
	KeySHA256       string `json:"key_sha256"`
}

func (d DeadLetter) Key() string {
	return fmt.Sprintf("%s/%d/%d", d.SourceTopic, d.SourcePartition, d.SourceOffset)
}

func (s Store) WorkerReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	var ready bool
	err := s.DB.QueryRowContext(ctx, `SELECT to_regclass('public.lab_jobs') IS NOT NULL
        AND to_regclass('public.lab_outbox') IS NOT NULL AND to_regclass('public.lab_job_effects') IS NOT NULL
        AND to_regclass('public.lab_worker_retries') IS NOT NULL
        AND to_regclass('public.lab_dead_letters') IS NOT NULL`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("worker reliability schema missing; apply migration 003")
	}
	return nil
}

func (s Store) RetryState(ctx context.Context, e Event) (RetryState, error) {
	return tracing.SQL(ctx, "worker.retry_state", func(ctx context.Context) (RetryState, error) { return s.retryState(ctx, e) })
}
func (s Store) retryState(ctx context.Context, e Event) (RetryState, error) {
	ctx, cancel := context.WithTimeout(ctx, database.OperationTimeout)
	defer cancel()
	var state RetryState
	var millis int64
	err := s.DB.QueryRowContext(ctx, `SELECT coalesce(r.failure_count,0),coalesce(r.failures_before_success,0),
        coalesce(greatest(0,ceil(extract(epoch FROM (r.next_retry_at-clock_timestamp()))*1000))::bigint,0)
        FROM public.lab_jobs j LEFT JOIN public.lab_worker_retries r ON r.job_id=j.id WHERE j.id=$1`, e.JobID).
		Scan(&state.Failures, &state.FailuresBeforeSuccess, &millis)
	state.Wait = time.Duration(millis) * time.Millisecond
	return state, err
}

// RecordFailure counts completed controlled work failures, not SQL/network
// failures or cancellations. CAS prevents overlapping consumers from counting
// the same attempt twice. A stale observer simply retries from persisted state.
func (s Store) RecordFailure(ctx context.Context, e Event, previous int) error {
	_, err := tracing.SQL(ctx, "worker.record_failure", func(ctx context.Context) (struct{}, error) { return struct{}{}, s.recordFailure(ctx, e, previous) })
	return err
}
func (s Store) recordFailure(ctx context.Context, e Event, previous int) error {
	if previous < 0 || previous >= MaxWorkerFailures {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, database.OperationTimeout)
	defer cancel()
	_, err := s.DB.ExecContext(ctx, `UPDATE public.lab_worker_retries SET failure_count=failure_count+1,
        next_retry_at=clock_timestamp()+($3::bigint * interval '1 millisecond')
        WHERE job_id=$1 AND failure_count=$2
        AND EXISTS(SELECT 1 FROM public.lab_jobs j WHERE j.id=$1 AND j.status='processing')`, e.JobID, previous, RetryDelay(int64(previous+1)).Milliseconds())
	return err
}

// ConfigureWorkerFault is a laboratory setup operation on an unprocessed job.
// The host command additionally requires all worker Pods to be stopped.
func (s Store) ConfigureWorkerFault(ctx context.Context, id string, failures int) error {
	if !ValidID(id) || failures < 1 || failures > MaxWorkerFailures {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, database.OperationTimeout)
	defer cancel()
	result, err := s.DB.ExecContext(ctx, `INSERT INTO public.lab_worker_retries(job_id,failures_before_success)
        SELECT id,$2 FROM public.lab_jobs WHERE id=$1 AND status IN ('accepted','published')
        ON CONFLICT(job_id) DO UPDATE SET failures_before_success=excluded.failures_before_success
        WHERE lab_worker_retries.failure_count=0`, id, failures)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("fault requires an existing unprocessed job without recorded failures")
	}
	return nil
}

// PrepareDeadLetter persists a deterministic envelope before Kafka I/O. A
// receipt already recorded after broker ack suppresses later republishing.
func (s Store) PrepareDeadLetter(ctx context.Context, d DeadLetter) (bool, error) {
	return tracing.SQL(ctx, "worker.dlq_prepare", func(ctx context.Context) (bool, error) { return s.prepareDeadLetter(ctx, d) })
}
func (s Store) prepareDeadLetter(ctx context.Context, d DeadLetter) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, database.OperationTimeout)
	defer cancel()
	body, err := json.Marshal(d)
	if err != nil {
		return false, err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO public.lab_dead_letters(source_topic,source_partition,source_offset,job_id,envelope)
        VALUES($1,$2,$3,nullif($4,'')::uuid,$5::jsonb) ON CONFLICT DO NOTHING`,
		d.SourceTopic, d.SourcePartition, d.SourceOffset, d.JobID, string(body))
	if err != nil {
		return false, err
	}
	var published, matches bool
	err = s.DB.QueryRowContext(ctx, `SELECT published_at IS NOT NULL,envelope=$4::jsonb FROM public.lab_dead_letters
        WHERE source_topic=$1 AND source_partition=$2 AND source_offset=$3`, d.SourceTopic, d.SourcePartition, d.SourceOffset, string(body)).Scan(&published, &matches)
	if err == nil && !matches {
		return false, ErrConflict
	}
	return published, err
}

func (s Store) DeadLetterPublished(ctx context.Context, d DeadLetter, partition int32, offset int64) error {
	_, err := tracing.SQL(ctx, "worker.dlq_mark.transaction", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, s.deadLetterPublished(ctx, d, partition, offset)
	})
	return err
}
func (s Store) deadLetterPublished(ctx context.Context, d DeadLetter, partition int32, offset int64) error {
	ctx, cancel := context.WithTimeout(ctx, database.OperationTimeout)
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // Best effort after primary error or successful commit.
	// Lock the job first, matching the lock order of normal effect transactions.
	if d.JobID != "" {
		var status string
		err = tx.QueryRowContext(ctx, `SELECT status FROM public.lab_jobs WHERE id=$1 FOR UPDATE`, d.JobID).Scan(&status)
		if err != nil {
			return err
		}
		if status == "succeeded" {
			return ErrConflict
		}
		var failures int
		if err := tx.QueryRowContext(ctx, `SELECT failure_count FROM public.lab_worker_retries WHERE job_id=$1`, d.JobID).Scan(&failures); err != nil {
			return err
		}
		if failures != MaxWorkerFailures {
			return ErrConflict
		}
		if _, err = tx.ExecContext(ctx, `UPDATE public.lab_jobs SET status='failed',error_code='retry_exhausted',
            completed_at=coalesce(completed_at,clock_timestamp()) WHERE id=$1`, d.JobID); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE public.lab_dead_letters SET published_at=coalesce(published_at,clock_timestamp()),
        dlq_partition=coalesce(dlq_partition,$4),dlq_offset=coalesce(dlq_offset,$5)
        WHERE source_topic=$1 AND source_partition=$2 AND source_offset=$3`, d.SourceTopic, d.SourcePartition, d.SourceOffset, partition, offset)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}
