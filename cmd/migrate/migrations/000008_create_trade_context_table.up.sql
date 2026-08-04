CREATE TABLE trade_context (
    id                  UUID            PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id            UUID            NOT NULL REFERENCES orders(id) ON DELETE CASCADE,

    signal_id           UUID,          
    market_state_id     UUID,           

    balance_before      NUMERIC(18,4),  
    equity_before       NUMERIC(18,4),  
    margin_used         NUMERIC(18,4),  

    created_at          TIMESTAMPTZ     DEFAULT NOW()
);

CREATE INDEX idx_trade_context_order ON trade_context(order_id);
CREATE INDEX idx_trade_context_signal ON trade_context(signal_id);
CREATE INDEX idx_trade_context_market ON trade_context(market_state_id);
