CREATE TABLE public.lab_jobs (
    id uuid PRIMARY KEY,
    idempotency_key varchar(64) NOT NULL UNIQUE,
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    job_type text NOT NULL CHECK (job_type = 'lab_effect'),
    payload jsonb NOT NULL CHECK (octet_length(payload::text) <= 1024),
    status text NOT NULL DEFAULT 'accepted'
        CHECK (status IN ('accepted', 'published', 'processing', 'succeeded', 'failed')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    completed_at timestamptz,
    error_code varchar(64)
);

CREATE TABLE public.lab_outbox (
    event_id uuid PRIMARY KEY,
    job_id uuid NOT NULL UNIQUE REFERENCES public.lab_jobs(id),
    topic text NOT NULL CHECK (topic = 'lab.jobs.v1'),
    schema_version integer NOT NULL CHECK (schema_version = 1),
    payload jsonb NOT NULL CHECK (octet_length(payload::text) <= 4096),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    published_at timestamptz,
    attempts bigint NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_retry_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    claim_token uuid,
    lease_until timestamptz,
    error_code varchar(64),
    kafka_partition integer,
    kafka_offset bigint,
    CHECK ((claim_token IS NULL) = (lease_until IS NULL))
);
CREATE INDEX lab_outbox_pending ON public.lab_outbox(next_retry_at, created_at, event_id)
    WHERE published_at IS NULL;

-- The worker is a later iteration; uniqueness is prepared, not proof of processing.
CREATE TABLE public.lab_job_effects (
    job_id uuid PRIMARY KEY REFERENCES public.lab_jobs(id),
    result jsonb NOT NULL CHECK (octet_length(result::text) <= 4096),
    committed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
