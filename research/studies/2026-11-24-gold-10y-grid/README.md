# Gold strategy grid over 10 years at real cost

Question: with the real cost per trade (not the 0.30 spread assumed before) and 10 years instead of 6, does any
strategy pass the scorecard?

## Method
`research/scripts/backtest_grid.sh` on `XAUUSD/2026-11-24_from2016` (2016-01 .. 2026-11), all 605 variants, live exit
settings (`BT_PD=off BT_LOCK=50:10,75:60,90:70 BT_INVAL=m5 BT_INVAL_RULE=erm2`). Cost per 1 oz: **$0.20** (0.12 live
average spread + 0.08 commission from prod fills) and **$0.40** (double). Scorecard: `research/scripts/scorecard.py
cost_0.20 cost_0.40 12 2016 20` — years positive >= 10 of 11, positive at double cost, neighbours positive,
n >= 300, 2026 median stop <= $20.

## Result
- 58 of 605 positive at $0.20; **24 positive at both $0.20 and $0.40 — all trend pullback (tfpb / tfpbng)**.
  **None passes every check.**
- Live strategies: trend_follow −31.8R, session_open −31.3R (11 years, $0.20).
- Short-stop near-misses collapse at double cost: best previous-day breakout +45.6R → −223.9R, ORB 09:30/15 +29.7R →
  −119.5R. Their stops were $2–3 in 2016–2019, so a fixed $ cost was 7–10% of risk; most profit came in 2023–2026 when
  gold's moves grew ~4x while the $ cost stayed flat.
- ORB 09:30/60 far ema200 without time stops: +55.5R at $0.20, +12.3R at $0.40, but ~0R in 2016–2020; 2026 stop ~$33.

## Best candidate: trend pullback
`tfpb_0.75_1.5_30` (H1 trend with ADX >= 30, price pulled back 0.75–1.5 H1 ATR from the recent extreme, enter when
M5 turns back with the trend; SL 1.5x / TP 2.5x M15 ATR):
+21.1R at $0.20 (9 of 11 years positive), +16.1R at $0.40 (7 of 11), positive in 2016–2020 and 2021–2026 at double
cost, neighbours positive. Fails: 223 trades (< 300), 2021 and 2025 negative, 2026 stop ~$25 (> $20). About 20 trades
a year, ~+$40–50 a year at 1 oz.
