# Do the dollar (EURUSD) or silver (XAGUSD) predict gold? 2016–2026

`crossasset.py` (full output in `output.txt`). Only past data at each step; only gold is traded; $0.20/oz per trade
plus swap on buys. Null = the other market shifted by whole weeks.

## 1. Co-movement and lead-lag (746,341 aligned 5-minute steps)
Same 5 minutes: corr(gold, EURUSD) +0.34, corr(gold, silver) +0.74. The other market now vs gold 5–30 minutes later:
−0.002 to +0.006 — no lead at 5-minute resolution; they move together, not one after the other.

## 2. Shock in EURUSD or silver (>= 4 sigma in 5 min) that gold has not followed
EURUSD: loses at every hold (15m −$215/−$165, 1h −$298/−$352 in 2016–21 / 2022–26), worse than the null.
Silver: only the 4h hold is positive (+$9 / +$910, 5 of 11 years) — nothing in 2016–2021, so not robust.

## 3/4. Gold moved >= 2 sigma more or less than the dollar/silver imply (beta from the past 20 days)
18 of 24 real configs lose in 2022–2026, like the null. The only one positive in both halves and better than its
null: silver, last 24h, hold 120h — +$494 / +$181, 7 of 11 years, 301 trades. One hit in 24 tries is what chance
gives, and a 5-day hold means large swings for a small account.

## Verdict
No usable edge. Gold, the dollar and silver react together within the same 5 minutes; by the time a 5-minute bot
sees the other market move, gold has already moved.
