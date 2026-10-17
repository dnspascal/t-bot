-- Every XAUUSD trade since 1 Aug with its realized result and close reason.
-- Session/month buckets and close-reason groups are applied in report generation (see report Method).
WITH c AS (
  SELECT f.our_position_id pid, string_agg(DISTINCT f.close_reason, '|') reason,
         SUM(f.net_profit) pnl, MAX(f.provider_exec_time) closed,
         SUM(f.execution_price * f.closed_volume) / NULLIF(SUM(f.closed_volume), 0) exit_px
  FROM fills f WHERE f.gross_profit IS NOT NULL AND f.closed_volume > 0 GROUP BY 1)
SELECT p.id, COALESCE(sig.strategy, '') strategy, p.side,
       p.open_timestamp AT TIME ZONE 'Africa/Dar_es_Salaam' opened,
       c.closed AT TIME ZONE 'Africa/Dar_es_Salaam' closed,
       c.reason, c.pnl, p.open_price, c.exit_px, o.sl, o.tp, p.max_favorable, p.max_adverse
FROM positions p
JOIN c ON c.pid = p.id
JOIN orders o ON o.id = p.our_order_id
JOIN symbols s ON s.id = p.symbol_id
LEFT JOIN signals sig ON sig.id = o.signal_id
WHERE s.symbol = 'XAUUSD' AND p.open_timestamp >= '2026-08-01 00:00+03'
ORDER BY p.open_timestamp;
