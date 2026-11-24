# Gold holding periods (4 hours to 4 weeks) and the re-open hour

Question: does any signal work when positions are held 4h–460 trading hours, after spread, commission and swap?

## Costs
$0.20 or $0.40 per trade. Swap from prod fills: buys pay ~$0.80/oz per night (0.019% of price; Wednesday x3), sells get
~$0.30 — modelled conservatively as buys pay (scaled with price), sells get nothing.

## Grid (`holding.py`, 1,224 runs on H1 2016–2026)
Momentum and fade (12h–60d lookback), EMA trend (50h–200d), Donchian breakout (1d–55d), each held 4/8/12/24/48/72/120/
240/460 trading hours with no stop or a one-day-range stop; plus buy or sell at each hour of the day for 4/8/12h.

- Buy and hold 1 oz, net of swap: +$1,119 over 2016–2026, negative in 2018, 2021, 2022, 2026. Most trend results are
  long-side gains from the 2016–2026 bull market; their sells are usually negative.
- Fade (mean reversion) loses at every horizon (7 of 126 positive).
- 4-hour holds mostly lose; 12h to 20 days are mostly positive but driven by buys.
- Donchian 55-day breakout is positive on both sides (median sells +$40), e.g. donch_55d_h48: +$1,887 at $0.20,
  +$1,851 at $0.40, 9 of 11 years, but max drawdown −$266 per oz.

## The re-open hour
Hour-by-hour, relative to the New York 17:00 close (the broker's daily break), 2016–2026 H1 bars:

| hour after re-open | mean | t-stat | years positive |
|---|---|---|---|
| 1st | +2.91 bp | +7.75 | 11/11 |
| 2nd | +0.89 bp | +2.94 | 11/11 |
| other hours | ~0 | | |

Checks with the bot's live bid/ask ticks: the history's first minute after re-open equals the live first bid exactly,
and on the 29 re-opens in the tick log the history bar and the live ticks agree (−$33.7 vs −$33.1). The re-open
spread is normal (~0.17). So it is not a bid-price artifact. But it has faded recently:

| period | 1st-hour bars | mean | positive |
|---|---|---|---|
| 2016–2025 | 2,575 | +2.90 bp | 63% |
| 2026 Jan–Jun | 129 | +6.75 bp | 51% |
| 2026 Jul–Nov | 103 | −1.75 bp | 40% |

Real buy-at-ask / sell-at-bid on the 29 live re-opens lost (buy 1 min after re-open, hold 1h: −$41; 4h: −$120).

## Verdict
The first hour after the daily re-open was a strong, consistent long bias for ten years, but it has been negative
since July 2026. Worth a forward test in shadow mode, not real money.
