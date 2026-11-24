# Gold in ranging markets

Question: when gold is ranging (low H1 ADX), can the bot fade the edges of a range — highs and lows over different
timeframes and sessions?

## 1. How often, and do edges hold? (`range_edges.py`, 2016–2026)
Gold has H1 ADX < 20 in 24–36% of hours every year (ADX < 25: 41–54%).

After an M5 bar pokes beyond a range edge and closes back inside (~33,500 events), which comes first: the range
middle, or 0.5 H1 ATR beyond the poke?

| range | ADX < 20 | ADX 20–25 | ADX >= 25 |
|---|---|---|---|
| last 12 H1 bars | 37% middle first | 34% | 33% |
| previous day | 35% | 33% | 32% |
| last 24 H1 bars | 29% | 26% | 20% |
| last 48 H1 bars | 23% | 19% | 11% |

Two out of three false breaks become real breaks, even in quiet markets; low ADX improves it by only 3–12 points.

## 2. Trading it (`analysis/backtest/rangefade.go`, 192 versions)
Fade an M5/M15 false break of the last 12/24/48 H1 bars, the previous day, the Asian (00–07 UTC) or London (07–12
UTC) range, only when H1 ADX < 20 or 25 and the range is >= 1 or 2 H1 ATR wide. Stop 0.25/0.5 H1 ATR beyond the poke,
target the middle or the far edge, live exit settings, cost $0.20/oz.

| range | positive | median | best |
|---|---|---|---|
| last 12 H1 | 0 / 32 | −382R | −160R |
| last 24 H1 | 0 / 32 | −196R | −88R |
| last 48 H1 | 2 / 32 | −55R | +20R |
| previous day | 0 / 32 | −172R | −61R |
| Asian session | 0 / 32 | −177R | −47R |
| London session | 0 / 32 | −74R | −18R |

The only positive version (rng_h48_adx20_M15_0.50_mid, +20.2R) is positive in 5 of 11 years, mostly 2024, and its
neighbours lose (plateau −44R). The double-cost run was stopped: nothing survives the real cost.

## Verdict
Fading range edges on gold does not work, with or without a low-ADX filter. The trend strategies standing aside on
ranging days (as on 2026-11-24) is the right behaviour.

## 3. Does a quiet morning predict a ranging afternoon? (`quiet_morning.sql`)
Morning = 00–12 UTC (03–15 EAT), afternoon = 12–21 UTC (15–24 EAT); quietness = morning range / average daily range
of the previous 20 days. 2,756 weekdays, 2016–2026.

| morning | days | afternoon stays inside | breaks one side | breaks both | breaks by >= half the morning range |
|---|---|---|---|---|---|
| very quiet (< 35% of ADR) | 286 | 2% | 56% | 42% | 80% |
| quiet (35–50%) | 847 | 3% | 70% | 27% | 64% |
| normal (50–70%) | 887 | 7% | 79% | 14% | 43% |
| busy (70%+) | 736 | 22% | 74% | 4% | 28% |

The opposite of the idea: a quiet morning almost never leads to a ranging afternoon (2–3% stay inside, every year
2016–2026). Quiet mornings are followed by expansion — the median break is 1.2x the morning range — and very quiet
mornings break both sides 42% of the time, so the direction is close to a coin flip.
