# Gold scalping on real ticks

Question: can the bot open many trades per hour, close them within minutes, and profit after all broker costs?

## Costs (prod fills, 1 oz)
Commission $0.08 round trip ($0.04 open + $0.04 close); live spread average $0.12 (Jul–Nov). About **$0.20 per trade**.
No swap or conversion fee for trades closed the same day.

## Method (`research/cmd/scalp`)
6,193,940 real bid/ask ticks the bot recorded, 2026-07-09 .. 2026-11-24. BUY fills at ask, SELL at bid, 300 ms order
latency, TP/SL filled at the tick that crosses them, time stop at market, one trade at a time, $0.08 commission.
3,648 configs: momentum / reversal on $0.5–2.5 moves within 15–120 s, breakout / fade of the 5–30 minute high-low,
TP and SL $0.3–2.0, max hold 1–15 min, all hours or 10:00–22:00 EAT. Chosen on 9 Jul–15 Sep, judged on 16 Sep–24 Nov.
Validated first on a random walk with a fixed 0.12 spread: median −$0.120 gross / −$0.200 net per trade, as expected.

## Result
**0 of 3,648 configs profitable**, in either half (a random walk gives 159 by luck).

| family | median gross / trade | median net / trade | best net / trade |
|---|---|---|---|
| momentum | −0.167 | −0.247 | −0.160 |
| reversal | −0.099 | −0.179 | −0.112 |
| breakout | −0.182 | −0.262 | −0.188 |
| fade | −0.090 | −0.170 | −0.084 |

- Chasing short bursts (momentum, breakout) loses more than the spread: after a burst plus 300 ms, the fill is worse.
- Fading 30-minute extremes in active hours earns about +$0.10 per trade before spread (gross −0.01 to −0.03 with the
  spread included), half of the $0.20 cost. Best config still lost ~$390–480 per 1 oz over each half.
- The most active configs trade ~3,000 times a day and lose ~$0.19–0.27 each.

## Verdict
Scalping gold on this account is not viable: the per-trade cost ($0.20) is about twice the best raw edge found.
