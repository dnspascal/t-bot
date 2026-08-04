CREATE TABLE market_states (
    id              UUID            NOT NULL DEFAULT gen_random_uuid(),
    symbol_id       UUID            NOT NULL REFERENCES symbols(id),
    provider        TEXT            NOT NULL DEFAULT 'ctrader', 
    period          TEXT            NOT NULL,  

    open            NUMERIC(12,5),
    high            NUMERIC(12,5),
    low             NUMERIC(12,5),
    close           NUMERIC(12,5),
    volume          BIGINT,

    ema_fast        NUMERIC(12,5),              
    ema_slow        NUMERIC(12,5),              
    rsi             NUMERIC(6,2),               
    adx             NUMERIC(6,2),               
    atr             NUMERIC(12,5),              

    support_level   NUMERIC(12,5),              
    resistance_level NUMERIC(12,5),             
    trend_high      NUMERIC(12,5),              
    trend_low       NUMERIC(12,5),              
    breakout_level  NUMERIC(12,5),              

    regime          TEXT,                       
    volatility_trend TEXT,                      
    momentum_direction TEXT,                    

    volume_ma       BIGINT,                     

    processing_us   BIGINT DEFAULT 0,           

    indicators      JSONB,

    bar_time        TIMESTAMPTZ     NOT NULL,  
    created_at      TIMESTAMPTZ     DEFAULT NOW(),

    PRIMARY KEY (id, bar_time),
    UNIQUE(symbol_id, provider, period, bar_time)
);

SELECT create_hypertable('market_states', 'bar_time');

CREATE INDEX idx_market_states_symbol ON market_states(symbol_id, provider, period, bar_time DESC);
CREATE INDEX idx_market_states_regime ON market_states(symbol_id, regime, bar_time DESC) WHERE regime IS NOT NULL;
CREATE INDEX idx_market_states_adx ON market_states(symbol_id, adx, bar_time DESC);
CREATE INDEX idx_market_states_rsi ON market_states(symbol_id, rsi, bar_time DESC);
