# trend_follow only on big days (2016–2026)

Lead from research/studies/2026-11-25-gold-exhaustion: trend_follow's trades on days whose range so far was >= 1.5x
the 20-day average range (ADR) were its best. `analysis/backtest/bigday.go` wraps the real trend_follow (it still
sees every bar, so streaks and cooldowns are as live) and only lets an entry through when today's range so far is
>= k x ADR20. Day = UTC day or New York trading day. Gap fills and swap on; live exit settings.

## Results (R; max open 2 as live)

| version | trades | @ $0.20 | @ $0.40 | 2016–21 | 2022–26 | years + | max DD |
|---|---|---|---|---|---|---|---|
| trend_follow (no filter) | 3,129 | −46.0 | −138.7 | −102.9 | +56.9 | 3/11 | −132.5 |
| utc, k 1.25 | 1,218 | +36.5 | −4.9 | −20.7 | +57.2 | 6/11 | −43.7 |
| utc, k 1.50 | 759 | +24.9 | −4.8 | −8.3 | +33.2 | 6/11 | −30.1 |
| **utc, k 1.75** | 513 | **+50.2** | **+25.4** | +9.2 | +41.0 | **9/11** | −20.8 |
| utc, k 2.00 | 338 | +33.7 | +18.4 | +8.0 | +25.8 | 8/11 | −11.6 |
| ny, k 1.50 | 541 | +39.9 | +11.7 | −1.6 | +41.5 | 6/11 | −15.0 |
| ny, k 1.75 | 331 | +24.4 | +9.1 | +3.6 | +20.8 | 7/11 | −13.5 |

k = 1.0 loses with both day definitions; k 1.5–2.0 is positive with both at $0.20, and k 1.75–2.0 at $0.40.
With 1 open trade the totals are roughly halved, same pattern.

Random-day baseline (same number of random trend_follow days, 2,000 draws): utc k1.75 beats 99.3% (2016–21 alone:
96.9%), utc k2.0 95.8%, ny k1.5 97.0%, ny k1.75 92.2%. Picking big days is better than picking days at random.

Caveats: most of the profit is 2025–2026 (utc k1.75: +34R of +50R); 2016–2024 averaged ~+1.8R a year. It trades
~47 times a year (trend_follow: ~285). On 2026-11-25 it would have blocked all eight trend_follow sells — the
range only reached 1.45x ADR ($148.5 vs $102.8) — including the +$95 of winners and the two −$23 losers
(trend_follow netted +$25 that day).

## Verdict
A real but modest improvement: the big-day filter turns trend_follow from −46R into +25 to +50R over ten years
with a sixth of its drawdown, but it trades rarely and leans on the recent high-volatility years.
