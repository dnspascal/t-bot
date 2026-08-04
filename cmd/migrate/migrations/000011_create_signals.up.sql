CREATE TABLE signals (
    id              UUID            NOT NULL DEFAULT gen_random_uuid(),
    symbol_id       UUID            NOT NULL REFERENCES symbols(id),
    provider        TEXT            NOT NULL DEFAULT 'ctrader',

    signal          TEXT            NOT NULL CHECK (signal IN ('BUY', 'SELL', 'HOLD')),

    checked_market_states JSONB,

    confluence      INT             DEFAULT 0,  
    confidence      NUMERIC(5,2),               

    processing_us   BIGINT          NOT NULL DEFAULT 0,  

    -- Timing
    bar_time        TIMESTAMPTZ,                         
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW(),

    PRIMARY KEY (id, created_at)
);

SELECT create_hypertable('signals', 'created_at');

CREATE INDEX idx_signals_symbol     ON signals(symbol_id, created_at DESC);
CREATE INDEX idx_signals_actionable ON signals(symbol_id, created_at DESC) WHERE signal != 'HOLD';
CREATE INDEX idx_signals_confluence ON signals(symbol_id, confluence DESC);
CREATE INDEX idx_signals_confidence ON signals(symbol_id, confidence DESC) WHERE confidence IS NOT NULL;
CREATE INDEX idx_signals_jsonb      ON signals USING GIN(checked_market_states);
