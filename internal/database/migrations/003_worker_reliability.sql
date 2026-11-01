CREATE TABLE public.lab_worker_retries (
    job_id uuid PRIMARY KEY REFERENCES public.lab_jobs(id) ON DELETE CASCADE,
    failures_before_success integer NOT NULL DEFAULT 0 CHECK (failures_before_success BETWEEN 0 AND 3),
    failure_count integer NOT NULL DEFAULT 0 CHECK (failure_count BETWEEN 0 AND 3),
    next_retry_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- Poison records need not have a valid job. Identity is the original Kafka
-- location, never a newly generated event ID or the DLQ's own offset.
CREATE TABLE public.lab_dead_letters (
    source_topic text NOT NULL CHECK (source_topic = 'lab.jobs.v1'),
    source_partition integer NOT NULL CHECK (source_partition BETWEEN 0 AND 2),
    source_offset bigint NOT NULL CHECK (source_offset >= 0),
    job_id uuid REFERENCES public.lab_jobs(id) ON DELETE CASCADE,
    envelope jsonb NOT NULL CHECK (octet_length(envelope::text) <= 4096),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    published_at timestamptz,
    dlq_partition integer,
    dlq_offset bigint,
    PRIMARY KEY (source_topic, source_partition, source_offset),
    CHECK ((published_at IS NULL AND dlq_partition IS NULL AND dlq_offset IS NULL)
        OR (published_at IS NOT NULL AND dlq_partition IS NOT NULL AND dlq_offset IS NOT NULL
            AND dlq_partition BETWEEN 0 AND 2 AND dlq_offset >= 0))
);
