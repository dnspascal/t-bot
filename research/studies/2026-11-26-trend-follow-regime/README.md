# trend_follow by H1 regime at entry (2016–2026)

Prompted by 2026-11-25: trend_follow's 4 losers were entered with H1 regime "breakout", its 4 winners with
"trending_down".

## By regime (3,129 trend_follow trades, $0.20)
breakout 2,074 trades +0.1R; trending_down 577, −3.0R; trending_up 478, −37.2R (−0.078R each, 2 of 11 years
positive). Breakout and trending_down entries are the same over time; the 25 Nov split was chance.

## Filters (`analysis/backtest/regimefilter.go`; gap fills, swap, max 2 open)

| version | trades | @ $0.20 | @ $0.40 | 2016–21 | 2022–26 | years + | max DD |
|---|---|---|---|---|---|---|---|
| trend_follow | 3,129 | −46.0 | −138.7 | −102.9 | +56.9 | 3/11 | −132.5 |
| skip buys in trending_up | 2,711 | −1.8 | −91.3 | −77.1 | +75.3 | 3/11 | −98.5 |
| skip sells in trending_down (mirror) | 2,616 | −44.5 | −131.0 | −101.7 | +57.2 | 3/11 | −135.1 |
| only breakout regime | 2,198 | −0.2 | −83.5 | −75.8 | +75.6 | 3/11 | −100.7 |
| trend_follow_bigday | 513 | +50.2 | +25.4 | +9.2 | +41.0 | 9/11 | −20.8 |
| bigday + skip buys in trending_up | 464 | +61.6 | +40.8 | +18.3 | +43.4 | 9/11 | −19.0 |

Random baseline: the 478 trending_up buys lose more than 95.4% of random 478-trade sets — but 2016–2021 alone only
64.5% (not significant) and 2022–2026 97.6%. The mirror filter does nothing.

## Verdict
Skipping buys in a trending_up H1 regime takes trend_follow from −46R to about break-even at real cost (still −91R at
double cost) — not enough to make it worth trading. On trend_follow_bigday it removes 49 trades and adds +11R (+15R
at double cost); the evidence is mostly 2022–2026.
