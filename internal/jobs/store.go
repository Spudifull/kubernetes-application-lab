package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/database"
	"github.com/Spudifull/kubernetes-application-lab/internal/tracing"
)

type Store struct{ DB *sql.DB }

// OutboxStats is a bounded snapshot, not a scrape-time query. Pending includes
// leased/retrying rows; only a durable published_at removes an event.
func (s Store) OutboxStats(ctx context.Context) (int64, float64, error) {
	ctx, cancel := context.WithTimeout(ctx, database.OperationTimeout)
	defer cancel()
	var pending int64
	var age float64
	err := s.DB.QueryRowContext(ctx, `SELECT count(*),
        COALESCE(GREATEST(EXTRACT(EPOCH FROM (clock_timestamp()-min(created_at))),0),0)::double precision
        FROM public.lab_outbox WHERE published_at IS NULL`).Scan(&pending, &age)
	return pending, age, err
}

func (s Store) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	var ready bool
	err := s.DB.QueryRowContext(ctx, `SELECT to_regclass('public.lab_jobs') IS NOT NULL
        AND to_regclass('public.lab_outbox') IS NOT NULL AND to_regclass('public.lab_job_effects') IS NOT NULL
        AND EXISTS(SELECT FROM information_schema.columns WHERE table_schema='public'
          AND table_name='lab_outbox' AND column_name='trace_parent')`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("jobs schema missing")
	}
	return nil
}

// Enqueue commits the job and its event together. ON CONFLICT waits for a
// concurrent insertion; the following READ COMMITTED statement sees its commit.
func (s Store) Enqueue(ctx context.Context, request Request) (Job, bool, error) {
	var created bool
	job, err := tracing.SQL(ctx, "jobs.enqueue.transaction", func(ctx context.Context) (Job, error) {
		job, fresh, err := s.enqueue(ctx, request)
		created = fresh
		return job, err
	})
	return job, created, err
}

func (s Store) enqueue(ctx context.Context, request Request) (Job, bool, error) {
	if err := request.Validate(); err != nil {
		return Job{}, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, database.OperationTimeout)
	defer cancel()
	id, err := newID()
	if err != nil {
		return Job{}, false, err
	}
	eventID, err := newID()
	if err != nil {
		return Job{}, false, err
	}
	hash := request.Hash()
	payload, err := json.Marshal(request.Payload)
	if err != nil {
		return Job{}, false, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Job{}, false, err
	}
	defer tx.Rollback() // Best effort after commit or any failed statement.
	job := Job{ID: id, JobType: request.JobType, Status: "accepted"}
	err = tx.QueryRowContext(ctx, `INSERT INTO public.lab_jobs(id,idempotency_key,request_hash,job_type,payload)
        VALUES ($1,$2,$3,$4,$5::jsonb) ON CONFLICT (idempotency_key) DO NOTHING RETURNING created_at`,
		id, request.IdempotencyKey, hash[:], request.JobType, string(payload)).Scan(&job.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		var existingHash []byte
		err = tx.QueryRowContext(ctx, `SELECT id::text,job_type,status,created_at,request_hash FROM public.lab_jobs WHERE idempotency_key=$1`,
			request.IdempotencyKey).Scan(&job.ID, &job.JobType, &job.Status, &job.CreatedAt, &existingHash)
		if err != nil {
			return Job{}, false, err
		}
		if !bytes.Equal(existingHash, hash[:]) {
			return Job{}, false, ErrConflict
		}
		if err = tx.Commit(); err != nil {
			return Job{}, false, err
		}
		return job, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	event, err := json.Marshal(Event{1, eventID, id, request.JobType, job.CreatedAt.UTC(), request.Payload})
	if err != nil {
		return Job{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO public.lab_outbox(event_id,job_id,topic,schema_version,payload,trace_parent)
        VALUES ($1,$2,$3,1,$4::jsonb,$5)`, eventID, id, Topic, string(event), tracing.Parent(ctx))
	if err != nil {
		return Job{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return Job{}, false, err
	}
	return job, true, nil
}

func (s Store) Get(ctx context.Context, id string) (Job, error) {
	return tracing.SQL(ctx, "jobs.get", func(ctx context.Context) (Job, error) { return s.get(ctx, id) })
}

func (s Store) get(ctx context.Context, id string) (Job, error) {
	ctx, cancel := context.WithTimeout(ctx, database.OperationTimeout)
	defer cancel()
	var job Job
	var completed sql.NullTime
	var code sql.NullString
	var result []byte
	err := s.DB.QueryRowContext(ctx, `SELECT j.id::text,j.job_type,j.status,j.created_at,j.completed_at,j.error_code,e.result
        FROM public.lab_jobs j LEFT JOIN public.lab_job_effects e ON e.job_id=j.id WHERE j.id=$1`, id).
		Scan(&job.ID, &job.JobType, &job.Status, &job.CreatedAt, &completed, &code, &result)
	if err != nil {
		return Job{}, err
	}
	if completed.Valid {
		job.CompletedAt = &completed.Time
	}
	if code.Valid {
		job.ErrorCode = &code.String
	}
	job.Result = result
	return job, nil
}

type Claim struct {
	EventID, JobID, Topic, Token string
	TraceParent                  string
	Payload                      []byte
	Attempt                      int64
}

// Claim holds a row lock for one statement, never during Kafka I/O. The token
// fences late acknowledgements. A crash is recovered after the 60-second lease.
func (s Store) Claim(ctx context.Context) (Claim, error) {
	ctx, cancel := context.WithTimeout(ctx, database.OperationTimeout)
	defer cancel()
	token, err := newID()
	if err != nil {
		return Claim{}, err
	}
	c := Claim{Token: token}
	err = s.DB.QueryRowContext(ctx, `WITH candidate AS (
        SELECT event_id FROM public.lab_outbox
        WHERE published_at IS NULL AND next_retry_at<=clock_timestamp()
          AND (lease_until IS NULL OR lease_until<=clock_timestamp())
        ORDER BY next_retry_at,created_at,event_id LIMIT 1 FOR UPDATE SKIP LOCKED
    ) UPDATE public.lab_outbox o SET claim_token=$1,lease_until=clock_timestamp()+interval '60 seconds',attempts=o.attempts+1
      FROM candidate c WHERE o.event_id=c.event_id
      RETURNING o.event_id::text,o.job_id::text,o.topic,o.payload,o.attempts,o.trace_parent`, token).
		Scan(&c.EventID, &c.JobID, &c.Topic, &c.Payload, &c.Attempt, &c.TraceParent)
	return c, err
}

func (s Store) Published(ctx context.Context, c Claim, partition int32, offset int64) error {
	_, err := tracing.SQL(ctx, "outbox.mark_published.transaction", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, s.published(ctx, c, partition, offset)
	})
	return err
}

func (s Store) published(ctx context.Context, c Claim, partition int32, offset int64) error {
	ctx, cancel := context.WithTimeout(ctx, database.OperationTimeout)
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // Best effort after the primary error or successful commit.
	result, err := tx.ExecContext(ctx, `UPDATE public.lab_outbox SET published_at=clock_timestamp(),claim_token=NULL,
        lease_until=NULL,error_code=NULL,kafka_partition=$3,kafka_offset=$4
        WHERE event_id=$1 AND claim_token=$2 AND published_at IS NULL AND lease_until>clock_timestamp()`, c.EventID, c.Token, partition, offset)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrLeaseLost
	}
	// The worker may already have committed a more advanced state before this ack.
	_, err = tx.ExecContext(ctx, `UPDATE public.lab_jobs SET status='published' WHERE id=$1 AND status='accepted'`, c.JobID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func RetryDelay(attempt int64) time.Duration {
	if attempt < 1 {
		return time.Second
	}
	if attempt >= 6 {
		return 30 * time.Second
	}
	return time.Second * time.Duration(1<<uint(attempt-1))
}

func (s Store) Retry(ctx context.Context, c Claim) error {
	_, err := tracing.SQL(ctx, "outbox.schedule_retry", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, s.retry(ctx, c)
	})
	return err
}

func (s Store) retry(ctx context.Context, c Claim) error {
	ctx, cancel := context.WithTimeout(ctx, database.OperationTimeout)
	defer cancel()
	result, err := s.DB.ExecContext(ctx, `UPDATE public.lab_outbox SET claim_token=NULL,lease_until=NULL,
        next_retry_at=clock_timestamp()+($3::bigint * interval '1 millisecond'),error_code='kafka_publish_failed'
        WHERE event_id=$1 AND claim_token=$2 AND published_at IS NULL AND lease_until>clock_timestamp()`,
		c.EventID, c.Token, RetryDelay(c.Attempt).Milliseconds())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}
