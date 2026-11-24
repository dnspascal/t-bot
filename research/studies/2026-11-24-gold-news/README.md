# Gold after US news shocks (2016–2026)

`news_shocks.py`. No calendar: at 08:30, 10:00 and 14:00 New York time, a shock is a first-5-minute move >= Z times the
median 5-minute move of the previous 2 hours. Enter at T+5m (as the bot can) in the shock direction, hold 1/2/4h,
$0.20/oz.

| shock | events | same direction after 2h | hold 2h net |
|---|---|---|---|
| >= 3x | 1,912 | 48% | −$300 |
| >= 5x | 831 | 50% | +$44 |
| >= 8x | 436 | 53% | +$197 |

08:30 releases are a coin flip at every size and hold. Shocks >= 5x were positive in only 5 of 11 years (2023 carries
the total). The one exception, 14:00 (Fed) shocks >= 8x, continued 63–68% of the time (+$217 to +$363) but on only
72 events — too few to trust after this many tests.

Verdict: by the time the bot can enter, the news move is priced in.
