# Can gold's exhaustion be seen at the time? (2016–2026)

Prompted by 2026-11-25: trend_follow sold at 18:25/18:30 EAT after a ~$125 fall (H1 RSI 25.5, 4.7 H1 ATR below the
EMA50) and gold rebounded ~$100.

`exhaustion.py` (output in `output.txt`). Event: at an H1 close, the last 24h moved >= 3 H1 ATR one way and price is
within 0.3 ATR of that extreme (2,855 events). Outcome on the M5 path: 1.5 ATR back first (reversal) or 1 ATR
further first (continuation). Features use only data up to the event.

## Result
Reversal first 40%, continuation first 60% — in both 2016–2021 (42%) and 2022–2026 (38%). That is exactly what a
random walk gives for +1 vs −1.5 (1.5 / 2.5 = 60%): after a stretched move, gold is no more likely to turn than
to keep going.

No classic exhaustion sign changes that:

| sign | reversal first when present | when absent |
|---|---|---|
| RSI divergence | 42% | 40% |
| volume >= 2x average | 39% | 41% |
| day's range so far >= 1.5x ADR | 35–37% | 41% |
| H1 RSI >= 80 (in the move's direction) | 32% | 40–41% |
| M5 RSI >= 85 | 31% (48 events) | 40–41% |
| 5+ H1 candles in a row | 40% | 40–42% |
| 2–3 signs together | 36–38% | 41% |

If anything, the extreme readings lean slightly toward continuation, not reversal. A rejection wick >= 50% showed
22% but on only 23 events.

## A leak caught on the way
The first run used the whole day's range for "day's range so far", including hours after the event. It showed big
days continuing 71–77% of the time in every year. Using only the range up to the event, the effect disappears
(35–37%). Kept here as a reminder of how a look-ahead makes a pattern look real.

## Verdict
On gold, an exhausted move and a move about to continue look the same at the time. This is why trend_follow's late
entries and the counter-trend strategies' early entries both lose: the turn is not visible in advance.

## Each sign as a filter on trend_follow (`filters.py`, output in `filters_output.txt`)
Block a trend_follow entry when the sign is present (closed bars only; history spliced with the bot's own candles
for 2026-11-25). trend_follow 2016–2026: 3,129 trades, −40.1R unfiltered.

| filter | stops 18:25 / 18:30 | blocked | their R | trend_follow with filter |
|---|---|---|---|---|
| stretched 3 ATR in 24h | no / no | 1,288 | −3.4 | −36.7R |
| RSI divergence | no / no | 526 | +10.9 | −51.0R |
| volume >= 2x | no / no | 696 | −11.0 | −29.1R |
| rejection wick >= 50% | **yes / yes** | 299 | +9.7 | −49.8R |
| day range >= 1.5x ADR | no / no | 558 | +49.1 | −89.2R |
| H1 RSI >= 80 (sell <= 20) | no / no | 366 | −7.7 | −32.4R |
| M5 RSI >= 85 (sell <= 15) | no / no | 74 | +1.0 | −41.1R |
| 5+ H1 candles in a row | no / no | 337 | −13.7 | −26.4R |
| >= 4 ATR beyond EMA50 | no / no | 1,298 | +0.1 | −40.2R |

Only the wick filter would have stopped both of today's sells, and over ten years it makes trend_follow worse. No
filter turns trend_follow positive. The best (5+ candles, volume) trim the loss by blocking trades that average
−0.02 to −0.04R — within noise.

Lead, not a finding: the trades on days whose range so far is >= 1.5x ADR were trend_follow's best (558 trades,
+49.1R, 8 of 11 years positive), but +44.4R of it is 2022–2026 and +40.4R is 2025–2026. Needs its own test before use.
