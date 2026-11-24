# Testing the two pattern-search candidates for leaks (gold 2016–2026)

## Leak checks built in
- **Look-ahead:** start of month counts weekdays from a calendar fixed in advance (not the data's trading days);
  evening dip acts only on the M5 close that is also the H1 close, entering at the next price.
- **Fills:** buys at ask (bid + cost), exits on bid; stops gapped through fill at the bar open (`BT_GAPFILL`).
- **Costs:** $0.20 and $0.40 per trade; buys pay swap per New York 17:00 rollover, Wednesday x3 (`BT_SWAP`).
- **Bot rules:** first M5 close after the re-open, live profit lock, EOD close, max hold (`strategy.MaxHolder`).
- **Bull-market drift:** permutation tests against random days / random hours (`permutation.py`).
- **Independent data:** the same start-of-month rule on 10 years of oil.

## Start of month — passes
Buy at the first M5 close after the re-open on the first N weekdays of the month, close before the next break.

- Permutation: first 3 weekdays +6,905 bp vs random 3-day picks median +1,923 bp; beats 99.7% of 2,000 random picks;
  both halves strong (2016–20 +3,284, 2021–26 +3,622 bp).
- Bot-rules backtest, 24 versions (days 1/2/3/5, stop 1/2/3 H1 ATR, $25 cap on/off): 23 positive at $0.20 and all
  23 still positive at $0.40 (median +$684 / +$633 per oz).
- `ms_d3_k1.0_captrue` (first 3 weekdays, stop 1x H1 ATR, skip if > $25): 372 trades, +$882 at $0.20, +$809 at
  $0.40, 9 of 11 years positive, max drawdown −$92 at 1 oz, Jul–Nov 2026 +$382. 2016 and 2017 were negative
  (−$17, −$51) and half the profit is 2026 (+$460). With 2 or 3 ATR stops the $25 cap skips every 2026 trade.
- Oil: beats only 76% of random picks and was negative 2021–2026 — not a general commodity effect.

## Evening dip — fails
Strong down H1 candle opening 10–13 New York, buy, hold 2–4h, exit before the break: 7 of 84 versions positive at
$0.20, 1 at both costs, median −$263. Permutation: 801 events averaged −1.67 bp, worse than 94% of random same-hour
picks. The search hit was a fragile definition.

## Verdict
Start of month is the most robust pattern found in this research. Its risk for an $85 account is the drawdown: −$92
at 1 oz is more than the whole balance.
