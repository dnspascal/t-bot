CREATE TABLE orders (
    id                    UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    signal_id             UUID,          
    provider              TEXT          NOT NULL DEFAULT 'ctrader', 

    provider_order_id     TEXT,          
    provider_position_id  TEXT,          

    symbol_id             UUID          NOT NULL REFERENCES symbols(id),
    side                  TEXT          NOT NULL CHECK (side IN ('BUY', 'SELL')),
    volume                BIGINT        NOT NULL,   
    sl                    NUMERIC(12,5),
    tp                    NUMERIC(12,5),
    entry_price           NUMERIC(12,5),            
    slippage_points       BIGINT,                   

    status                TEXT          NOT NULL DEFAULT 'pending',
    error_code            TEXT,          
    error_msg             TEXT,

    sent_at               TIMESTAMPTZ,  
    execution_received_at TIMESTAMPTZ,  
    round_trip_ms         BIGINT,       

    created_at            TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_orders_status           ON orders (status);
CREATE INDEX idx_orders_symbol_id        ON orders (symbol_id, created_at DESC);
CREATE INDEX idx_orders_signal           ON orders (signal_id);
CREATE INDEX idx_orders_provider_order   ON orders (provider, provider_order_id);
CREATE INDEX idx_orders_provider_pos     ON orders (provider, provider_position_id);
