package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Spudifull/kubernetes-application-lab/internal/database"
	"github.com/Spudifull/kubernetes-application-lab/internal/tracing"
)

// effectTransaction serializes writers on the job row, including overlapping
// consumers after a lost session. Kafka network I/O and the simulated work are
// outside this short transaction; the PK on lab_job_effects is the final guard.
func (s Store) effectTransaction(ctx context.Context, e Event, complete bool) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, database.OperationTimeout)
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() // Best effort; preserve the primary error or commit result.
	var typ, status, eventID string
	var payload []byte
	var created time.Time
	err = tx.QueryRowContext(ctx, `SELECT j.job_type,j.payload,j.created_at,j.status,o.event_id::text
        FROM public.lab_jobs j JOIN public.lab_outbox o ON o.job_id=j.id
        WHERE j.id=$1 FOR UPDATE OF j`, e.JobID).Scan(&typ, &payload, &created, &status, &eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrEvent
	}
	if err != nil {
		return false, err
	}
	var p Payload
	if json.Unmarshal(payload, &p) != nil || p != e.Payload || typ != e.JobType || eventID != e.EventID || !created.Equal(e.CreatedAt) {
		return false, ErrEvent
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.lab_job_effects WHERE job_id=$1)`, e.JobID).Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		if status != "succeeded" {
			return false, ErrEvent
		}
		return true, tx.Commit()
	}
	if status != "accepted" && status != "published" && status != "processing" {
		if status == "failed" {
			return false, ErrJobFailed
		}
		return false, ErrEvent
	}
	if complete {
		result, err := json.Marshal(Effect{Outcome: "completed", DurationMS: e.Payload.DurationMS})
		if err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO public.lab_job_effects(job_id,result) VALUES ($1,$2::jsonb)`, e.JobID, string(result)); err != nil {
			return false, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE public.lab_jobs SET status='succeeded',completed_at=clock_timestamp(),error_code=NULL WHERE id=$1`, e.JobID)
		if err != nil {
			return false, err
		}
	} else {
		if _, err = tx.ExecContext(ctx, `UPDATE public.lab_jobs SET status='processing' WHERE id=$1`, e.JobID); err != nil {
			return false, err
		}
	}
	return false, tx.Commit()
}

func (s Store) Start(ctx context.Context, e Event) (bool, error) {
	return tracing.SQL(ctx, "jobs.start.transaction", func(ctx context.Context) (bool, error) { return s.effectTransaction(ctx, e, false) })
}
func (s Store) Complete(ctx context.Context, e Event) (bool, error) {
	return tracing.SQL(ctx, "jobs.complete.transaction", func(ctx context.Context) (bool, error) { return s.effectTransaction(ctx, e, true) })
}
