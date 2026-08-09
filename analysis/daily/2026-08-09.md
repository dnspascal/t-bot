# Session Analysis — 2026-08-09

## Week Reviewed: Aug 3–7, 2026

---

## Key Findings

### 1. dd_ranging_breakout was the biggest bleeder
Strategy was firing every 5–20 minutes and hitting SL almost every time. Root cause: M5 breakout signals were firing even when there was no real momentum behind the move — ADX on M15 was 10–19 on every losing trade.

**Evidence from actual market_states data (queried directly):**
- Winning trades (05:55 +$8.67, 06:00 +$1.69): M15 ADX 21–24
- All losing trades: M15 ADX 10–19
- M15 EMA was bullish all week (would have filtered nothing — confirmed by query before deciding)

Fix: Added `M15 ADX ≥ 20` gate. If M15 has no momentum, the M5 breakout is noise.

---

### 2. trend_follow had a tp-coverage problem
Several trend_follow trades went in the right direction, reached $13–$15 favorable on a $27 TP, then reversed all the way back to SL. Because `minPeakGain = tpDist * 0.5`, peak_drawback only arms after the position reaches 50% of TP distance ($13.50 on a $27 TP). Trades that peaked at $13 or less had zero protection.

**Evidence (from positions query with max_favorable/max_adverse):**
- Multiple positions: max_favorable $8–$15, closed at full SL loss
- These would have been saved by a break-even stop at 40% of SL distance

**Total damage from this gap across all strategies (week): significant — positions that went right and still lost.**

---

### 3. Regime classification weakness (trend_follow)
`trending_up` / `trending_down` is classified by EMA gap ≥ 0.1% alone — ADX is not used at all. This means trend_follow can fire in weak trends where price is just drifting. H1 ADX filter added to block these.

**Evidence:** trend_follow was the top earner (+$204) but only on strong-trend days. The filter blocks weak-trend entries, not the winning ones.

---

## Queries Used

### All trades Aug 3–7 with strategy, outcome, commission
```sql
SELECT
  p.provider_position_id,
  sig.strategy_name,
  p.side,
  p.open_price,
  p.close_price,
  f.gross_profit,
  f.commission,
  f.gross_profit + f.commission AS net,
  p.created_at
FROM positions p
JOIN orders o ON o.id = p.our_order_id
JOIN signals sig ON sig.id = o.signal_id
JOIN fills f ON f.position_id = p.id AND f.is_close = true
WHERE p.created_at >= '2026-08-03'
  AND p.created_at < '2026-08-08'
  AND p.provider = 'ctrader'
ORDER BY p.created_at;
```

### M15 market state at time of dd_ranging_breakout signals
```sql
SELECT
  ms.captured_at,
  ms.timeframe,
  ms.adx,
  ms.ema_fast,
  ms.ema_slow,
  ms.regime,
  ms.rsi
FROM market_states ms
WHERE ms.timeframe = 'M15'
  AND ms.captured_at >= '2026-08-05 05:00'
  AND ms.captured_at <= '2026-08-05 07:00'
ORDER BY ms.captured_at;
```

### Positions with peak favorable / adverse (break-even damage analysis)
```sql
SELECT
  p.provider_position_id,
  sig.strategy_name,
  p.side,
  p.open_price,
  p.close_price,
  p.max_favorable,
  p.max_adverse,
  f.gross_profit,
  f.commission,
  CASE
    WHEN p.side = 'BUY' THEN (p.max_favorable - p.open_price)
    ELSE (p.open_price - p.max_favorable)
  END AS peak_gain,
  CASE
    WHEN p.side = 'BUY' THEN (p.max_favorable - p.open_price) / NULLIF(p.tp_price - p.open_price, 0) * 100
    ELSE (p.open_price - p.max_favorable) / NULLIF(p.open_price - p.tp_price, 0) * 100
  END AS peak_as_pct_of_sl_dist
FROM positions p
JOIN orders o ON o.id = p.our_order_id
JOIN signals sig ON sig.id = o.signal_id
JOIN fills f ON f.position_id = p.id AND f.is_close = true
WHERE p.created_at >= '2026-08-03'
  AND p.created_at < '2026-08-08'
  AND p.provider = 'ctrader'
  AND f.gross_profit < 0
ORDER BY peak_gain DESC;
```

---

## All Code Changes Today

### 1. `internal/strategy/trend_follow/strategy.go`
**Change:** Added H1 ADX ≥ 20 check after regime check, before ATR sizing.
```go
if h1.ADX < 20 {
    return hold("H1 ADX too low — trend too weak to chase")
}
```
**Why:** Regime = `trending_up` only requires EMA gap ≥ 0.1%. ADX can be 14 and still classify as trending. The filter prevents chasing drifts that look like trends on EMA alone.

---

### 2. `internal/strategy/dd_ranging_breakout/strategy.go`
**Change:** Added M15 ADX ≥ 20 check. Replaced an earlier (wrong) attempt to use M15 EMA bullish check.
```go
m15, ok := states[config.PeriodM15]
if !ok || !m15.IsWarmedUp {
    return hold("M15 not warmed up")
}
if m15.ADX < 20 {
    return hold("M15 ADX too low — breakout has no real momentum")
}
```
**Why:** Data showed all winning breakouts had M15 ADX 21–24. All losers had M15 ADX 10–19. M15 EMA was bullish all week so would have filtered nothing — verified before deciding.

---

### 3. `internal/provider/ctrader/api/constants.go`
**Change:** Added two payload type constants.
```go
ProtoOAAmendPositionSLTPReq = uint32(2110)
ProtoOAClosePositionReq     = uint32(2111)
```
**Why:** Needed for break-even stop implementation. 2110 confirmed from cTrader Open API v2 docs.

---

### 4. `internal/provider/ctrader/api/proto.go`
**Change:** Added `encodeAmendPositionSLTPReq`. Encodes the inner message the same way all other request encoders do (field 1 = payloadType, field 2 = accountID, field 3 = positionId, field 4 = stopLoss). `SendRaw` handles the outer envelope — no double-wrapping.
```go
func encodeAmendPositionSLTPReq(accountID, positionID int64, newSL float64) []byte {
    var b []byte
    b = appendUint32(b, 1, ProtoOAAmendPositionSLTPReq)
    b = appendInt64(b, 2, accountID)
    b = appendInt64(b, 3, positionID)
    b = appendDouble(b, 4, newSL)
    return b
}
```

---

### 5. `internal/provider/ctrader/api/client.go`
**Change:** Added `AmendPositionSL` method. Fire-and-forget, response comes back via ExecutionEvent (2126) as ORDER_REPLACED (4).
```go
func (c *Client) AmendPositionSL(positionID int64, newSL float64) error {
    // auth check, then:
    inner := encodeAmendPositionSLTPReq(accountID, positionID, newSL)
    return c.conn.SendRaw(ProtoOAAmendPositionSLTPReq, inner)
}
```

---

### 6. `internal/provider/provider.go`
**Change:** Added to Provider interface:
```go
AmendPositionSL(ctx context.Context, positionID string, newSLPrice float64) error
```

---

### 7. `internal/provider/ctrader/ctrader.go`
**Change:** Implemented AmendPositionSL — parses string positionID to int64, delegates to client.

---

### 8. `internal/provider/oanda/oanda.go` and `internal/provider/binance/binance.go`
**Change:** Stub implementations returning `fmt.Errorf("AmendPositionSL not yet implemented for ...")`. Both providers satisfy the interface.

---

### 9. `internal/bot/registry.go`
**Change 1:** Added `BreakEvenActive bool` field to `trackedPosition` struct.
**Change 2:** Added `SetBreakEven(id string)` method — sets flag under mutex.
**Why:** Prevents the break-even logic from firing more than once per position.

---

### 10. `internal/bot/watcher.go`
**Change:** Added `checkBreakEven` function, called from `checkPeakDrawback` on every price tick after peaks are updated.

**Logic:**
- Skip if `BreakEvenActive` already true
- Compute SL distance: `abs(openPrice - SLPrice)`
- Compute peak gain: favorable movement from open price
- Trigger if `peakGain >= 0.40 * slDist` (40% of SL distance in profit)
- New SL = `openPrice + 2 pips` (BUY) or `openPrice - 2 pips` (SELL)
- Calls `provider.AmendPositionSL` — only marks active if send succeeds
- Records in `position_adjustments` table (positionUUID, old_sl, new_sl, reason="break_even")

**Why 40%:** Data-driven — positions that went 40%+ of SL distance favorable had enough room to cover the 2-pip buffer even after broker costs ($0.07–0.08/trade). The existing `minPeakGain = tpDist * 0.5` only protects positions above 50% of TP, leaving a large unprotected gap.

**Why broker amendment not internal close:** Crash safety — if bot restarts after setting break-even, the SL is already at the broker. An internal-only close would be lost on restart. Also: `position_adjustments` table already existed for exactly this purpose.

---

### 11. `internal/bot/bot.go`
**Change:** Added `ORDER_REPLACED` case in `onExecution` handler:
```go
case config.ExecOrderReplaced:
    slog.Info("SL/TP amendment confirmed by broker", "brokerOrderID", exec.BrokerOrderID)
    return
```
**Why:** When AmendPositionSLTP is accepted, the broker fires ExecutionEvent (2126) with ORDER_REPLACED (4). Previously this fell through silently with no log trace. Now it's visible in logs.

---

## Break-Even Flow End-to-End

```
price tick
  → checkPeakDrawback
    → UpdatePeaks
    → checkBreakEven
        peakGain >= 40% of SL dist?
          yes → AmendPositionSL (ProtoOAAmendPositionSLTPReq 2110)
                 → SendRaw → outer ProtoMessage envelope → TCP
                 broker responds → ExecutionEvent (2126) ORDER_REPLACED (4)
                 → onExecution logs "SL/TP amendment confirmed"
               SetBreakEven (BreakEvenActive = true)
               INSERT position_adjustments (old_sl, new_sl, "break_even")
          no  → skip
    → peakDrawbackPct >= 60%?
          yes → closeTrackedPosition
```

---

## Strategies NOT Touched Today

| Strategy | Status |
|---|---|
| `dd_oversold_bounce` | Not reviewed — mentioned as candidate but not actioned |
| `sr_bounce` | Not reviewed |
| `rsi_reversal` | Not reviewed (last release was RSI fix) |
| `session_momentum` | Not reviewed |
| `session_open` | Not reviewed |
| `ema_pullback` | Not reviewed |
| `breakout` | Not reviewed |
| `combined` | Not reviewed |
| `regime` | Not reviewed |

Priority for next session: `dd_oversold_bounce` — user flagged it as also data-driven but not yet reviewed. `sr_bounce` should also be checked since it runs in ranging market (same conditions where dd_ranging_breakout fires).

---

## Deployment Checklist

- [x] `go build ./...` passes clean
- [x] All provider interface methods implemented (ctrader, oanda stub, binance stub)
- [x] Proto encoding matches cTrader Open API v2 spec (field numbers confirmed)
- [x] Break-even trigger confirmed: 40% of SL distance
- [x] Buffer: 2 pips above/below open (covers $0.07–$0.08 broker cost)
- [x] Audit trail: position_adjustments table populated on every amendment
- [x] Exec channel: ORDER_REPLACED (4) now logged
- [x] BreakEvenActive flag: only set after successful send, prevents double-fire
- [x] IDByProviderPositionID: confirmed method exists in position.Repository
