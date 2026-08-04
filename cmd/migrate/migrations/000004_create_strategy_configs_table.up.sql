CREATE TABLE strategy_configs (
    id              UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    symbol_id       UUID            NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    strategy_name   TEXT            NOT NULL,  -- combined_ema_rsi, range_trader, etc

    -- combined_ema_rsi: {"fast_period": 9, "slow_period": 21, "rsi_period": 14, "rsi_overbought": 70, "rsi_oversold": 30, ...}
    -- range_trader:     {"lookback_bars": 20, "support_offset": 10, "resistance_offset": 10, ...}
    config          JSONB           NOT NULL,

    enabled         BOOLEAN         DEFAULT true,

    created_at      TIMESTAMPTZ     DEFAULT NOW(),
    updated_at      TIMESTAMPTZ     DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ,    -- soft delete

    UNIQUE(symbol_id, strategy_name),
    CHECK (deleted_at IS NULL OR updated_at <= deleted_at)
);

CREATE INDEX idx_strategy_configs_symbol ON strategy_configs(symbol_id);
CREATE INDEX idx_strategy_configs_active ON strategy_configs(symbol_id, strategy_name) WHERE deleted_at IS NULL AND enabled = true;
