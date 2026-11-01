-- Additive migration: old binaries remain compatible. Do not modify 001-003.
-- Diagnostic context is not event content and is excluded from request_hash.
ALTER TABLE public.lab_outbox
    ADD COLUMN trace_parent varchar(55) NOT NULL DEFAULT '';
ALTER TABLE public.lab_outbox
    ADD CONSTRAINT lab_outbox_trace_parent_format
    CHECK (trace_parent = '' OR trace_parent ~ '^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$');
