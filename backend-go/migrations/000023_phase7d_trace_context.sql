ALTER TABLE outbox_events
    ADD COLUMN IF NOT EXISTS traceparent VARCHAR(55),
    ADD COLUMN IF NOT EXISTS tracestate VARCHAR(512);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'outbox_events_traceparent_check') THEN
        ALTER TABLE outbox_events ADD CONSTRAINT outbox_events_traceparent_check
            CHECK (traceparent IS NULL OR traceparent ~ '^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$');
    END IF;
END $$;
