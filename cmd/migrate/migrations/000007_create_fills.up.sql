CREATE TABLE fills (
    id                   UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    our_order_id         UUID          REFERENCES orders(id),    
    our_position_id      UUID          REFERENCES positions(id), 
    provider             TEXT          NOT NULL DEFAULT 'ctrader', 
    provider_fill_id     TEXT          NOT NULL,   
    provider_order_id    TEXT,                     
    provider_position_id TEXT,                     
    symbol_id            UUID          NOT NULL REFERENCES symbols(id),
    side                 TEXT          NOT NULL CHECK (side IN ('BUY', 'SELL')),

    volume               BIGINT,        
    filled_volume        BIGINT,        
    execution_price      NUMERIC(12,5), 
    event_type           TEXT          NOT NULL,   
    fill_status          TEXT,                     

    commission           NUMERIC(18,4),
    margin_rate          NUMERIC(12,8), 
    base_to_usd_rate     NUMERIC(12,8), 

    close_entry_price    NUMERIC(12,5), 
    close_reason         TEXT,          
    gross_profit         NUMERIC(18,4), 
    close_swap           NUMERIC(18,4), 
    close_commission     NUMERIC(18,4), 
    balance_after        NUMERIC(18,4), 
    closed_volume        BIGINT,        
    pnl_conversion_fee   NUMERIC(18,4), 
    trade_duration_ms    BIGINT,  
          
    net_profit           NUMERIC(18,4) GENERATED ALWAYS AS (
                             COALESCE(gross_profit, 0)
                             + COALESCE(close_commission, 0)
                             + COALESCE(close_swap, 0)
                             + COALESCE(pnl_conversion_fee, 0)
                         ) STORED,

    provider_create_time TIMESTAMPTZ,
    provider_exec_time   TIMESTAMPTZ,

    raw_payload          BYTEA,         

    received_at          TIMESTAMPTZ   NOT NULL DEFAULT NOW(),

    UNIQUE (provider, provider_fill_id)
);

CREATE INDEX idx_fills_our_order_id    ON fills (our_order_id);
CREATE INDEX idx_fills_our_position_id ON fills (our_position_id);
CREATE INDEX idx_fills_provider_pos    ON fills (provider, provider_position_id);
CREATE INDEX idx_fills_symbol          ON fills (symbol_id, received_at DESC);
CREATE INDEX idx_fills_event_type      ON fills (event_type, received_at DESC);
