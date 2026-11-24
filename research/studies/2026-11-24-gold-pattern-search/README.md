# Systematic pattern search on gold (2016–2026)

`search.py`: 14 features on every H1 bar (session, weekday, RSI, ADX, EMA50/200 trend, distance from EMA50, 4h/24h/5d
move, volatility regime, position in the 24h range, previous-day high/low, last candle shape, start of month). Every
single feature bin and every pair (~1,500 conditions) x buy/sell x hold 1/4/24h. Direction chosen on 2016–2021,
judged on 2022–2026; trades never overlap; $0.20/oz per trade plus swap on buys. Null: the same search with forward
returns shifted by whole weeks (3 runs).

| screen on 2016–2021 | real: pass screen | real: also profitable 2022–26 (4+/5 yrs) | null: pass screen | null: also OOS |
|---|---|---|---|---|
| strict (t >= 3, 5+/6 years) | 1 | 1 | 0, 0, 0 | 0, 0, 0 |
| loose (t >= 2, 4+/6 years) | 13 | 8 | 21, 9, 18 | 3, 1, 1 |

Indicator conditions and pairs carry no edge beyond chance. The out-of-sample survivors are two effects:

- **Start of month** (6 of 8 survivors are variants): buy each of the first 3 trading days, hold 24h — +$403 in
  2016–2021, +$1,270 in 2022–2026, 5 of 5 years positive. Same effect as research/studies/2026-11-24-gold-calendar.
- **Evening dip:** a strong down H1 candle between 17:00 and 22:00 UTC, buy, hold 4h — +$361 / +$981. Split by
  whether the hold crosses the daily re-open: crossing, 1,129 trades, +$494, mostly 2026 (+$475); not crossing
  (entries early enough to exit before the break), 364 trades, +$500, 61% win, positive 9 of 11 years.

Both need a proper bot-rules backtest (entry timing, stops) before trading.
