CREATE TABLE bot_events (
    id          UUID        NOT NULL DEFAULT gen_random_uuid(),
    event_type  TEXT        NOT NULL,  -- free-form: started | stopped | auth_ok | order_sent | error | ...
    detail      JSONB,       -- structured data: side, volume, price, error, latency, etc.
    elapsed_ms  BIGINT      NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, created_at)
);

SELECT create_hypertable('bot_events', 'created_at');

CREATE INDEX idx_bot_events_type   ON bot_events (event_type, created_at DESC);
CREATE INDEX idx_bot_events_errors ON bot_events (created_at DESC) WHERE event_type = 'error';
CREATE INDEX idx_bot_events_detail ON bot_events USING GIN (detail);
