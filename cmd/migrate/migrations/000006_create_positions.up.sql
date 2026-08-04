CREATE TABLE positions (
    id                   UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    our_order_id         UUID          REFERENCES orders(id),  
    provider             TEXT          NOT NULL DEFAULT 'ctrader', 
    provider_position_id TEXT          NOT NULL,   
    provider_acct_id     TEXT          NOT NULL,   
    symbol_id            UUID          NOT NULL REFERENCES symbols(id),
    side                 TEXT          NOT NULL CHECK (side IN ('BUY', 'SELL')),
    volume               BIGINT        NOT NULL,   
    tier                 SMALLINT      NOT NULL DEFAULT 0, 

    open_price           NUMERIC(12,5),            
    current_sl           NUMERIC(12,5),            
    current_tp           NUMERIC(12,5),            


    max_favorable        NUMERIC(12,5),
    max_adverse          NUMERIC(12,5),

    swap                 NUMERIC(18,4) NOT NULL DEFAULT 0,
    commission           NUMERIC(18,4) NOT NULL DEFAULT 0,
    used_margin          NUMERIC(18,4),

    status               TEXT          NOT NULL DEFAULT 'created',
    trailing_stop_loss   BOOL          NOT NULL DEFAULT FALSE,
    guaranteed_stop_loss BOOL          NOT NULL DEFAULT FALSE,

    label                TEXT,
    comment              TEXT,

    open_timestamp       TIMESTAMPTZ,
    close_timestamp      TIMESTAMPTZ,

    raw_payload          BYTEA,        

    created_at           TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ   NOT NULL DEFAULT NOW(),

    UNIQUE (provider, provider_position_id)
);

CREATE INDEX idx_positions_status      ON positions (status) WHERE status = 'open';
CREATE INDEX idx_positions_symbol_id   ON positions (symbol_id, created_at DESC);
CREATE INDEX idx_positions_provider_id ON positions (provider, provider_position_id);
