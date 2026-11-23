CREATE TABLE IF NOT EXISTS shadow_trades (
    id            UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    signal_id     UUID,
    symbol_id     UUID          NOT NULL REFERENCES symbols(id),
    strategy      TEXT          NOT NULL,
    side          TEXT          NOT NULL,
    volume        BIGINT        NOT NULL,
    entry_price   NUMERIC(12,5) NOT NULL,
    sl            NUMERIC(12,5) NOT NULL,
    tp            NUMERIC(12,5) NOT NULL,
    current_sl    NUMERIC(12,5) NOT NULL,
    lock_stage    INT           NOT NULL DEFAULT 0,
    max_favorable NUMERIC(12,5),
    max_adverse   NUMERIC(12,5),
    opened_at     TIMESTAMPTZ   NOT NULL,
    closed_at     TIMESTAMPTZ,
    close_price   NUMERIC(12,5),
    close_reason  TEXT,
    pnl_per_oz    NUMERIC(12,5),
    pnl_usd       NUMERIC(12,5),
    created_at    TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_shadow_trades_strategy ON shadow_trades (strategy, opened_at DESC);
CREATE INDEX IF NOT EXISTS idx_shadow_trades_open ON shadow_trades (symbol_id) WHERE closed_at IS NULL;

CREATE OR REPLACE VIEW all_trades AS
SELECT 'live'::text AS mode, p.id, sig.strategy::text AS strategy, p.symbol_id, p.side::text AS side, p.volume::bigint AS volume,
       p.open_price::numeric AS entry_price, o.sl::numeric AS sl, o.tp::numeric AS tp,
       p.open_timestamp AS opened_at, c.closed_at, c.close_price::numeric AS close_price, c.close_reason,
       (c.gross / NULLIF(c.vol / 100.0, 0))::numeric AS pnl_per_oz, c.net::numeric AS pnl_usd
FROM positions p
JOIN orders o ON o.id = p.our_order_id
LEFT JOIN signals sig ON sig.id = o.signal_id
LEFT JOIN LATERAL (
    SELECT MAX(f.provider_exec_time) AS closed_at,
           (ARRAY_AGG(f.execution_price ORDER BY f.provider_exec_time DESC))[1] AS close_price,
           STRING_AGG(DISTINCT f.close_reason, '|') AS close_reason,
           SUM(f.gross_profit) AS gross, SUM(f.closed_volume) AS vol, SUM(f.net_profit) AS net
    FROM fills f
    WHERE f.our_position_id = p.id AND f.gross_profit IS NOT NULL AND f.closed_volume > 0
) c ON true
UNION ALL
SELECT 'shadow'::text, s.id, s.strategy, s.symbol_id, s.side, s.volume,
       s.entry_price::numeric, s.sl::numeric, s.tp::numeric,
       s.opened_at, s.closed_at, s.close_price::numeric, s.close_reason,
       s.pnl_per_oz::numeric, s.pnl_usd::numeric
FROM shadow_trades s;
