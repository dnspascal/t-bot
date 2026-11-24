# Data validation: is the downloaded gold history real?

Question: can the cTrader XAUUSD history used for backtesting be trusted?

## 1. Two downloads a week apart (`compare.sh`)

`2026-11-17_from2021` vs `2026-11-24_from2021_copy2`, M1/M5/M15/H1 over the shared range:
2,664,085 candles, **100% identical** (OHLC and volume), none missing on either side.
Download is reproducible; cTrader does not revise history.

## 2. History vs what the live bot recorded (prod, read-only)

Exports: `data/prod/candles.tsv` (bot candles), `data/prod/ticks_m1.tsv` (bot ticks per minute, bid OHLC + spread).

**Bot candles** (`candles_vs_history.sql`):
- Before 2026-11-06 the bot stored close = low on 100% of candles (live trendbars carry no close; fixed in 00c8d96,
  deployed v202611062040). Open/high/low matched history on every candle.
- After the fix: 21,000+ candles across M1–H4 match history **exactly**; one M1 candle differs by $0.31.

**Bot ticks** (from 2026-07-09; ticks 2–8 July contain other metals' prices, 124k rows, excluded):
- 130,669 minutes compared. The live tick range sits inside the history candle 99.67% of the time.
- Close diff median $0.04, p95 $0.38 (ticks are stamped on receipt, so minute edges shift slightly).

## 3. Real spread vs the backtest's 0.30

Live gold spread Jul–Nov: average **0.12**, p95 of minutes 0.17, max 1.46. Widest 23:00–01:00 EAT (~0.17),
tightest 19:00–22:00 EAT (~0.10). The backtest's 0.30 is about 2.5x the real average, so it is conservative
on spread. Commission and slippage are not in these numbers.

## Verdict

The history is the same feed the bot trades on and is safe to backtest on. Data error is ruled out as the reason
the strategies failed the 6-year scorecard.
