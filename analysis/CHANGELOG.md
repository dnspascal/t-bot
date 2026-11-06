# Strategy Changelog

## 2025-07-07 — Remove session filter

**Change:** Removed the London/NY session filter. Bot now trades all hours including Tokyo (23:00–06:59 UTC). Only the dead session (22:00–22:59 UTC) is still blocked via the existing EOD window.

**Why:** v1 baseline analysis (2025-07-05) showed that removing the session filter raised win rate from 44.6% to 47.2% (+2.6%) and added 37 more entries over 14 days. Tokyo-session setups within the combined strategy were winning more often than London/NY ones, which contradicted the original assumption about Tokyo being noisier.

**Data that justified this:** analysis/v1_baseline_2025-07-05/report.html — Test 2, "Remove session filter" row.

**What to watch:** Re-run Test 1 and Test 2 after 2–3 weeks of live data under this change. Create analysis/v2_no_session_YYYY-MM-DD/ with updated results.

**Files changed:** internal/bot/strategy.go, internal/bot/bot.go

## 2026-11-04 — trend_follow: only trade with the H1 EMA21/EMA50 trend

**Change:** trend_follow holds a BUY when H1 EMA21 ≤ EMA50 and a SELL when H1 EMA21 ≥ EMA50. Also holds until EMA50 is ready. London reopened for XAUUSD the same week.

**Why:** The profitable 3–9 Aug week traded with the H1 EMA21/EMA50 trend. Tested on past trades, the rule blocks 20 trades (2 winners, −114.78 combined) and turns Sep and Oct positive. Designed on Aug–Sep, tested on Oct–Nov.

**Data that justified this:** analysis/daily/2026-11-06/report.html — section 2.

**What to watch:** How often trend_follow holds because of the H1 EMA50 rule (signals.reason), and its P&L each month.

**Files changed:** internal/strategy/trend_follow/strategy.go, internal/bot/session.go

## 2026-11-06 — Live candles: close = bid instead of low

**Change:** Live trendbars without deltaClose now take their close from the spot bid (kept within the candle's low–high range).

**Why:** Every live candle had been saved with close = low since the first commit, so all indicators were calculated from lows.

**Data that justified this:** analysis/daily/2026-11-06/report.html — section 4.

**What to watch:** New candles no longer have close = low. Expect signal changes in ema_pullback, the RSI strategies and session_momentum. Redo the per-strategy P&L after a few days.

**Files changed:** internal/provider/ctrader/api/proto.go, internal/provider/ctrader/api/client.go
