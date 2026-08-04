CREATE TABLE account_snapshots (
    id               UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    provider         TEXT          NOT NULL DEFAULT 'ctrader', 
    provider_acct_id TEXT          NOT NULL,   

    balance          NUMERIC(18,4) NOT NULL,   
    leverage_ratio   NUMERIC(8,2),             
    max_leverage     NUMERIC(8,2),
    account_mode     TEXT,                     
    currency         TEXT,                     
    broker_name      TEXT,

    is_limited_risk  BOOL,
    fair_stop_out    BOOL,

    provider_payload JSONB,

    trigger          TEXT,                     -- startup | post_trade | scheduled

    snapshotted_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_account_snapshots ON account_snapshots (provider, provider_acct_id, snapshotted_at DESC);
