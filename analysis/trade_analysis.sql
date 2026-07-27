-- Trade analysis: entry conditions vs outcome
-- Run: sudo bash -c 'source /srv/tbot/.env && psql $DATABASE_URL -f analysis/trade_analysis.sql'

WITH trades AS (
  SELECT
    p.id         AS position_id,
    p.symbol_id,
    s.strategy,
    p.side,
    p.open_price,
    p.open_timestamp,
    p.close_timestamp,
    f.gross_profit,
    f.close_reason,
    ROUND(EXTRACT(EPOCH FROM (COALESCE(p.close_timestamp, NOW()) - p.open_timestamp))/60) AS duration_min,
    CASE WHEN f.gross_profit > 0 THEN 'WIN' WHEN f.gross_profit < 0 THEN 'LOSS' ELSE 'OPEN' END AS outcome,
    -- Market state IDs at signal time
    (s.checked_market_states->'M5' ->>'id')::uuid AS m5_id,
    (s.checked_market_states->'M15'->>'id')::uuid AS m15_id,
    (s.checked_market_states->'H1' ->>'id')::uuid AS h1_id,
    (s.checked_market_states->'H4' ->>'id')::uuid AS h4_id
  FROM positions p
  JOIN orders o ON o.id = p.our_order_id
  JOIN signals s ON s.id = o.signal_id
  LEFT JOIN fills f ON f.our_position_id = p.id AND f.event_type = 'close'
  WHERE p.open_timestamp >= '2026-07-17 00:00:00+03'
),
-- Entry conditions per timeframe
entry AS (
  SELECT
    t.position_id,
    t.strategy,
    t.side,
    t.open_price,
    t.open_timestamp,
    t.gross_profit,
    t.close_reason,
    t.duration_min,
    t.outcome,

    -- M5 at entry
    m5.regime        AS m5_regime,
    ROUND(m5.rsi::numeric, 1) AS m5_rsi,
    ROUND(m5.atr::numeric, 5) AS m5_atr,

    -- M15 at entry
    m15.regime       AS m15_regime,
    ROUND(m15.rsi::numeric, 1) AS m15_rsi,
    ROUND(m15.breakout_level::numeric, 5) AS m15_breakout_level,
    ROUND(m15.adx::numeric, 1) AS m15_adx,

    -- H1 at entry
    h1.regime        AS h1_regime,
    ROUND(h1.rsi::numeric, 1) AS h1_rsi,
    ROUND(h1.ema_fast::numeric, 5) AS h1_ema_fast,
    ROUND(h1.ema_slow::numeric, 5) AS h1_ema_slow,
    -- was price above or below H1 EMA50?
    CASE
      WHEN h1.close > h1.ema_slow THEN 'above_ema'
      ELSE 'below_ema'
    END AS h1_price_vs_ema,

    -- H4 at entry
    h4.regime        AS h4_regime,
    ROUND(h4.rsi::numeric, 1) AS h4_rsi

  FROM trades t
  LEFT JOIN market_states m5  ON m5.id  = t.m5_id
  LEFT JOIN market_states m15 ON m15.id = t.m15_id
  LEFT JOIN market_states h1  ON h1.id  = t.h1_id
  LEFT JOIN market_states h4  ON h4.id  = t.h4_id
),
-- Price movement after entry (M5 candles)
price_after AS (
  SELECT
    t.position_id,
    -- closest M5 bar at each interval after open
    (SELECT ms.close FROM market_states ms
     WHERE ms.symbol_id = t.symbol_id AND ms.period = 'M5'
       AND ms.bar_time >= t.open_timestamp + interval '5 minutes'
     ORDER BY ms.bar_time ASC LIMIT 1) AS price_t5m,
    (SELECT ms.close FROM market_states ms
     WHERE ms.symbol_id = t.symbol_id AND ms.period = 'M5'
       AND ms.bar_time >= t.open_timestamp + interval '15 minutes'
     ORDER BY ms.bar_time ASC LIMIT 1) AS price_t15m,
    (SELECT ms.close FROM market_states ms
     WHERE ms.symbol_id = t.symbol_id AND ms.period = 'M5'
       AND ms.bar_time >= t.open_timestamp + interval '30 minutes'
     ORDER BY ms.bar_time ASC LIMIT 1) AS price_t30m,
    (SELECT ms.close FROM market_states ms
     WHERE ms.symbol_id = t.symbol_id AND ms.period = 'M5'
       AND ms.bar_time >= t.open_timestamp + interval '1 hour'
     ORDER BY ms.bar_time ASC LIMIT 1) AS price_t1h,
    (SELECT ms.close FROM market_states ms
     WHERE ms.symbol_id = t.symbol_id AND ms.period = 'M5'
       AND ms.bar_time >= t.open_timestamp + interval '2 hours'
     ORDER BY ms.bar_time ASC LIMIT 1) AS price_t2h,
    (SELECT ms.close FROM market_states ms
     WHERE ms.symbol_id = t.symbol_id AND ms.period = 'M5'
       AND ms.bar_time >= t.open_timestamp + interval '4 hours'
     ORDER BY ms.bar_time ASC LIMIT 1) AS price_t4h,
    (SELECT ms.close FROM market_states ms
     WHERE ms.symbol_id = t.symbol_id AND ms.period = 'M5'
       AND ms.bar_time >= t.open_timestamp + interval '6 hours'
     ORDER BY ms.bar_time ASC LIMIT 1) AS price_t6h,
    (SELECT ms.close FROM market_states ms
     WHERE ms.symbol_id = t.symbol_id AND ms.period = 'M5'
       AND ms.bar_time >= t.open_timestamp + interval '10 hours'
     ORDER BY ms.bar_time ASC LIMIT 1) AS price_t10h
  FROM trades t
)
SELECT
  e.strategy,
  e.side,
  e.outcome,
  ROUND(e.gross_profit::numeric, 2) AS pnl,
  e.close_reason,
  e.duration_min,
  e.open_price,
  e.open_timestamp::date AS date,
  -- Entry conditions
  e.m5_regime, e.m5_rsi,
  e.m15_regime, e.m15_rsi, e.m15_adx,
  e.h1_regime, e.h1_rsi, e.h1_price_vs_ema,
  e.h4_regime,
  -- Price movement after open
  p.price_t5m,
  p.price_t15m,
  p.price_t30m,
  p.price_t1h,
  p.price_t2h,
  p.price_t4h,
  p.price_t6h,
  p.price_t10h
FROM entry e
JOIN price_after p ON p.position_id = e.position_id
ORDER BY e.strategy, e.outcome DESC, e.open_timestamp;
