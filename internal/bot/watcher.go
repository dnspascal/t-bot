package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	strat "github.com/denismgaya/t-bot/internal/strategy"
)

const peakDrawbackThreshold = 60.0
const peakDrawbackGatePct = 70.0
const neverProfitableTimeout = 30 * time.Minute

const signalsToClose = 3

const signalsToReduce = 2

const breakEvenTriggerPct = 33.0
const breakEvenBufferPips = 2.0

func decisionParams() map[string]any {
	return map[string]any{
		"peak_drawback_gate_pct":       peakDrawbackGatePct,
		"peak_drawback_threshold_pct":  peakDrawbackThreshold,
		"breakeven_trigger_pct":        breakEvenTriggerPct,
		"breakeven_buffer_pips":        breakEvenBufferPips,
		"signals_to_close":             signalsToClose,
		"signals_to_reduce":            signalsToReduce,
		"never_profitable_timeout_min": int(neverProfitableTimeout / time.Minute),
	}
}

func isEODWindow() bool {
	now := time.Now().UTC()
	wd := now.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return false
	}
	return now.Hour() == 21 && now.Minute() >= 30
}

func (b *Bot) watchPositions(ctx context.Context, ms indicator.MarketState) {
	if isEODWindow() {
		for _, pos := range b.registry.All() {
			if _, pending := b.pendingCloseReasons[pos.ProviderPositionID]; pending {
				continue
			}
			slog.Info("EOD close — 00:30 DSM, closing before dead session",
				"posID", pos.ProviderPositionID,
				"side", pos.Side,
			)
			b.closeTrackedPosition(ctx, pos, strat.CloseReasonEODClose)
		}
		return
	}

	for _, pos := range b.registry.All() {
		if pc, pending := b.pendingCloseReasons[pos.ProviderPositionID]; pending {
			if time.Since(pc.sentAt) < pendingCloseTimeout {
				continue
			}
			slog.Warn("pending close timed out — retrying if conditions still met",
				"posID", pos.ProviderPositionID, "reason", pc.reason,
			)
			delete(b.pendingCloseReasons, pos.ProviderPositionID)
		}

		b.registry.UpdatePeaks(pos.ProviderPositionID, ms.Close)

		pos, ok := b.registry.Get(pos.ProviderPositionID)
		if !ok {
			continue
		}

		if b.usesTrendWatcher(pos.StrategyName) {
			n, signals := countReversalSignals(ms, pos, b.pipSize)
			if n == 0 {
				continue
			}

			slog.Debug("reversal signals detected")

			reason := strings.Join(signals, ",")
			switch {
			case n >= signalsToClose:
				slog.Info("3+ signals confirmed — closing position",
					"posID", pos.ProviderPositionID, "n", n,
				)
				b.closeTrackedPosition(ctx, pos, reason)

			case n >= signalsToReduce && pos.Tier >= config.TierStronger:
				slog.Info("2 signals — reducing high-tier position",
					"posID", pos.ProviderPositionID, "tier", pos.Tier,
				)
				b.closeTrackedPosition(ctx, pos, reason)
			}
		}
	}
}

func (b *Bot) usesTrendWatcher(strategyName string) bool {
	for _, s := range b.strategies {
		if s.Name() == strategyName {
			return s.UsesTrendWatcher()
		}
	}
	return true // unknown strategy — safe default is to apply watcher
}

func countReversalSignals(ms indicator.MarketState, pos trackedPosition, pipSize float64) (int, []string) {
	var signals []string

	if pct := peakDrawbackPct(pos, ms.Close, pipSize); pct >= peakDrawbackThreshold {
		signals = append(signals, fmt.Sprintf(strat.PeakDrawbackPrefix+"%.0f%%", pct))
	}

	if (pos.Side == "BUY" && (ms.Regime == "trending_down" || ms.Regime == "ranging")) ||
		(pos.Side == "SELL" && (ms.Regime == "trending_up" || ms.Regime == "ranging")) {
		signals = append(signals, strat.ReversalRegimeAgainst)
	}

	if (pos.Side == "BUY" && ms.RSI < rsiMidline) ||
		(pos.Side == "SELL" && ms.RSI > rsiMidline) {
		signals = append(signals, strat.ReversalRSIAgainst)
	}

	if (pos.Side == "BUY" && ms.EMAFast < ms.EMASlow) ||
		(pos.Side == "SELL" && ms.EMAFast > ms.EMASlow) {
		signals = append(signals, strat.ReversalEMACrossAgainst)
	}

	if (pos.Side == "BUY" && ms.MomentumDirection == "falling") ||
		(pos.Side == "SELL" && ms.MomentumDirection == "rising") {
		signals = append(signals, strat.ReversalMomentumAgainst)
	}

	return len(signals), signals
}

func peakDrawbackPct(pos trackedPosition, currentPrice, pipSize float64) float64 {
	if pos.OpenPrice == 0 {
		return 0
	}

	var tpDist float64
	if pos.Side == config.SignalBuy {
		tpDist = pos.TPPrice - pos.OpenPrice
	} else {
		tpDist = pos.OpenPrice - pos.TPPrice
	}
	minPeakGain := tpDist * (peakDrawbackGatePct / 100)
	if minPeakGain <= 0 {
		minPeakGain = 3 * pipSize
	}

	var peakGain, currentGain float64
	if pos.Side == config.SignalBuy {
		peakGain = pos.MaxFavorable - pos.OpenPrice
		currentGain = currentPrice - pos.OpenPrice
	} else {
		peakGain = pos.OpenPrice - pos.MaxFavorable
		currentGain = pos.OpenPrice - currentPrice
	}

	if peakGain < minPeakGain {
		return 0
	}

	gaveBack := peakGain - currentGain
	if gaveBack <= 0 {
		return 0
	}

	return (gaveBack / peakGain) * 100
}

func (b *Bot) checkPeakDrawback(ctx context.Context, currentPrice float64) {
	b.checkTimeStop(ctx, currentPrice)
	for _, pos := range b.registry.All() {
		if pc, pending := b.pendingCloseReasons[pos.ProviderPositionID]; pending {
			if time.Since(pc.sentAt) < pendingCloseTimeout {
				continue
			}
			slog.Warn("pending close timed out — retrying",
				"posID", pos.ProviderPositionID, "reason", pc.reason,
			)
			delete(b.pendingCloseReasons, pos.ProviderPositionID)
		}

		b.registry.UpdatePeaks(pos.ProviderPositionID, currentPrice)
		pos, ok := b.registry.Get(pos.ProviderPositionID)
		if !ok {
			continue
		}

		b.checkBreakEven(ctx, pos)

		if pct := peakDrawbackPct(pos, currentPrice, b.pipSize); pct >= peakDrawbackThreshold {
			reason := fmt.Sprintf(strat.PeakDrawbackPrefix+"%.0f%%", pct)
			slog.Info("peak drawback — closing position",
				"posID", pos.ProviderPositionID,
				"side", pos.Side,
				"drawback", fmt.Sprintf("%.0f%%", pct),
			)
			b.closeTrackedPosition(ctx, pos, reason)
		}
	}
}

// The bot has no way to know an AmendPositionSLTPReq actually landed at the
// broker just because the network write succeeded — cTrader does not always
// respond, and on 2026-08-18 two trades (f21fd931, a69ed10c) lost real money
// after the bot logged "break-even stop set" for an amend that was never
// confirmed by a broker ORDER_REPLACED event: price ran straight through the
// supposed new stop with nothing closing the position, until the reversal
// watcher (watchPositions, a separate check) eventually market-closed it at
// a much worse price. See analysis/daily/2026-08-18 for the full trace.
//
// So break-even is now confirm-then-commit: send the amend, wait (off the
// main loop, so a slow/absent broker response can't stall other positions'
// M1 checks) for the ORDER_REPLACED execution event, and only mark
// BreakEvenActive / write position_adjustments once that confirmation
// actually arrives. If it doesn't arrive in time, retry a bounded number of
// times, and log loudly (not silently) once we give up — so "is this trade
// actually protected" is answered by a log line, not by reconstructing it
// after the fact from price data and position_adjustments like this time.
const breakEvenConfirmTimeout = 5 * time.Second
const breakEvenMaxAttempts = 3

func (b *Bot) checkBreakEven(ctx context.Context, pos trackedPosition) {
	if pos.BreakEvenActive || pos.BreakEvenPending || pos.BreakEvenGaveUp {
		return
	}

	tpDist := pos.TPPrice - pos.OpenPrice
	if pos.Side == config.SignalSell {
		tpDist = pos.OpenPrice - pos.TPPrice
	}
	if tpDist <= 0 {
		return
	}

	var peakGain float64
	if pos.Side == config.SignalBuy {
		peakGain = pos.MaxFavorable - pos.OpenPrice
	} else {
		peakGain = pos.OpenPrice - pos.MaxFavorable
	}

	if peakGain < (breakEvenTriggerPct/100)*tpDist {
		return
	}

	var newSL float64
	if pos.Side == config.SignalBuy {
		newSL = pos.OpenPrice + breakEvenBufferPips*b.pipSize
	} else {
		newSL = pos.OpenPrice - breakEvenBufferPips*b.pipSize
	}

	if err := b.provider.AmendPositionSL(ctx, pos.ProviderPositionID, newSL, pos.TPPrice); err != nil {
		slog.Error("break-even amend SEND FAILED — network/write error, not retried until next tick",
			"posID", pos.ProviderPositionID, "newSL", newSL, "err", err,
		)
		return
	}

	attempt := b.registry.IncrementBreakEvenAttempts(pos.ProviderPositionID)
	b.registry.SetBreakEvenPending(pos.ProviderPositionID, true)

	confirmCh := make(chan struct{})
	b.pendingBreakEvenMu.Lock()
	b.pendingBreakEven[pos.ProviderPositionID] = confirmCh
	b.pendingBreakEvenMu.Unlock()

	slog.Info("break-even amend SENT — awaiting broker confirmation",
		"posID", pos.ProviderPositionID,
		"side", pos.Side,
		"openPrice", pos.OpenPrice,
		"oldSL", pos.SLPrice,
		"newSL", newSL,
		"peakGain", peakGain,
		"tpDist", tpDist,
		"attempt", attempt,
		"maxAttempts", breakEvenMaxAttempts,
	)

	go b.awaitBreakEvenConfirmation(ctx, pos, newSL, peakGain, tpDist, attempt, confirmCh)
}

// awaitBreakEvenConfirmation runs off the main event loop so a slow or
// absent broker response can't stall M1 processing for other positions.
func (b *Bot) awaitBreakEvenConfirmation(ctx context.Context, pos trackedPosition, newSL, peakGain, tpDist float64, attempt int, confirmCh chan struct{}) {
	select {
	case <-ctx.Done():
		b.pendingBreakEvenMu.Lock()
		delete(b.pendingBreakEven, pos.ProviderPositionID)
		b.pendingBreakEvenMu.Unlock()
		return

	case <-confirmCh:
		slog.Info("break-even CONFIRMED by broker — position now protected",
			"posID", pos.ProviderPositionID, "newSL", newSL, "attempt", attempt,
		)
		b.registry.SetBreakEven(pos.ProviderPositionID)
		b.recordBreakEvenAdjustment(ctx, pos, newSL)

	case <-time.After(breakEvenConfirmTimeout):
		b.pendingBreakEvenMu.Lock()
		delete(b.pendingBreakEven, pos.ProviderPositionID)
		b.pendingBreakEvenMu.Unlock()

		if attempt >= breakEvenMaxAttempts {
			b.registry.SetBreakEvenGaveUp(pos.ProviderPositionID)
			slog.Error("break-even UNCONFIRMED after max attempts — GIVING UP, position is running on its ORIGINAL stop, not newSL",
				"posID", pos.ProviderPositionID, "attemptedNewSL", newSL, "originalSL", pos.SLPrice, "attempts", attempt,
			)
			return
		}

		b.registry.SetBreakEvenPending(pos.ProviderPositionID, false)
		slog.Warn("break-even UNCONFIRMED after timeout — no broker response, will retry on next check",
			"posID", pos.ProviderPositionID, "attemptedNewSL", newSL, "timeout", breakEvenConfirmTimeout, "attempt", attempt,
		)
	}
}

func (b *Bot) runTestAmend(ctx context.Context, pos trackedPosition) {
	var newSL float64
	if pos.Side == config.SignalBuy {
		newSL = pos.OpenPrice + breakEvenBufferPips*b.pipSize
	} else {
		newSL = pos.OpenPrice - breakEvenBufferPips*b.pipSize
	}

	confirmed := false
	for attempt := 1; attempt <= breakEvenMaxAttempts; attempt++ {
		if err := b.provider.AmendPositionSL(ctx, pos.ProviderPositionID, newSL, pos.TPPrice); err != nil {
			slog.Error("TESTAMEND: AmendPositionSL send FAILED",
				"posID", pos.ProviderPositionID, "newSL", newSL, "attempt", attempt, "err", err,
			)
			break
		}

		confirmCh := make(chan struct{})
		b.pendingBreakEvenMu.Lock()
		b.pendingBreakEven[pos.ProviderPositionID] = confirmCh
		b.pendingBreakEvenMu.Unlock()

		slog.Info("TESTAMEND: amend SENT — awaiting broker confirmation",
			"posID", pos.ProviderPositionID, "openPrice", pos.OpenPrice, "newSL", newSL,
			"attempt", attempt, "maxAttempts", breakEvenMaxAttempts,
		)

		select {
		case <-confirmCh:
			slog.Info("TESTAMEND: CONFIRMED by broker", "posID", pos.ProviderPositionID, "newSL", newSL, "attempt", attempt)
			confirmed = true
		case <-time.After(breakEvenConfirmTimeout):
			b.pendingBreakEvenMu.Lock()
			delete(b.pendingBreakEven, pos.ProviderPositionID)
			b.pendingBreakEvenMu.Unlock()
			slog.Warn("TESTAMEND: UNCONFIRMED after timeout — no broker response",
				"posID", pos.ProviderPositionID, "attempt", attempt, "timeout", breakEvenConfirmTimeout,
			)
		}
		if confirmed {
			break
		}
	}

	if !confirmed {
		slog.Error("TESTAMEND: never confirmed after max attempts — this is the exact gap real break-even hits",
			"posID", pos.ProviderPositionID, "attemptedNewSL", newSL,
		)
	}

	if livePos, ok := b.registry.Get(pos.ProviderPositionID); ok {
		slog.Info("TESTAMEND: closing test position (cleanup)", "posID", pos.ProviderPositionID, "confirmed", confirmed)
		b.closeTrackedPosition(ctx, livePos, "test_amend_cmd")
	}
}

func (b *Bot) signalBreakEvenConfirmed(providerPositionID string) {
	if providerPositionID == "" {
		return
	}
	b.pendingBreakEvenMu.Lock()
	ch, ok := b.pendingBreakEven[providerPositionID]
	if ok {
		delete(b.pendingBreakEven, providerPositionID)
	}
	b.pendingBreakEvenMu.Unlock()
	if ok {
		close(ch)
	}
}

func (b *Bot) recordBreakEvenAdjustment(ctx context.Context, pos trackedPosition, newSL float64) {
	posUUID, err := b.positions.IDByProviderPositionID(ctx, b.provider.Name(), pos.ProviderPositionID)
	if err != nil {
		slog.Warn("recordBreakEvenAdjustment: could not resolve position UUID for adjustment record",
			"posID", pos.ProviderPositionID, "err", err,
		)
		return
	}
	if _, err := b.db.Exec(ctx,
		`INSERT INTO position_adjustments (position_id, old_sl, new_sl, reason)
		 VALUES ($1, $2, $3, $4)`,
		posUUID, pos.SLPrice, newSL, "break_even",
	); err != nil {
		slog.Warn("recordBreakEvenAdjustment: failed to record adjustment in DB",
			"posUUID", posUUID, "err", err,
		)
	}
}

func (b *Bot) checkTimeStop(ctx context.Context, _ float64) {
	for _, pos := range b.registry.All() {
		if time.Since(pos.OpenTime) < neverProfitableTimeout {
			continue
		}
		if _, pending := b.pendingCloseReasons[pos.ProviderPositionID]; pending {
			continue
		}
		neverProfitable := false
		if pos.Side == "BUY" && pos.MaxFavorable <= pos.OpenPrice+b.pipSize {
			neverProfitable = true
		} else if pos.Side == "SELL" && pos.MaxFavorable >= pos.OpenPrice-b.pipSize {
			neverProfitable = true
		}
		if neverProfitable {
			slog.Info("time stop — position never profitable in 30m, closing",
				"posID", pos.ProviderPositionID,
				"side", pos.Side,
				"openPrice", pos.OpenPrice,
				"maxFavorable", pos.MaxFavorable,
			)
			b.closeTrackedPosition(ctx, pos, strat.CloseReasonTimeStopNeverProfitable30m)
		}
	}
}

func (b *Bot) logM1State(currentPrice float64) {
	positions := b.registry.All()
	count := len(positions)
	if count == 0 {
		return
	}
	provider := b.provider.Name()
	var totalUnrealized float64

	for _, pos := range positions {
		var unrealized float64
		if pos.Side == "BUY" {
			unrealized = b.unrealizedUSD(currentPrice-pos.OpenPrice, pos.Volume)
		} else {
			unrealized = b.unrealizedUSD(pos.OpenPrice-currentPrice, pos.Volume)
		}
		totalUnrealized += unrealized

	}

	if count > 1 {
		slog.Debug("M1 total P&L",
			"provider", provider,
			"positions", count,
			"totalPnlUSD", fmt.Sprintf("%+.4f", totalUnrealized),
		)
	}
}

func (b *Bot) closeTrackedPosition(ctx context.Context, pos trackedPosition, reason string) {
	if _, err := b.provider.ClosePosition(ctx, pos.ProviderPositionID, pos.Volume); err != nil {
		slog.Error("watcher: ClosePosition failed",
			"posID", pos.ProviderPositionID, "err", err,
		)

		if strings.Contains(err.Error(), "INCORRECT_BOUNDARIES") {
			slog.Warn("watcher: position already gone at broker — purging",
				"posID", pos.ProviderPositionID,
			)
			b.registry.Remove(pos.ProviderPositionID)
			if dbErr := b.positions.Close(ctx, b.provider.Name(), pos.ProviderPositionID, time.Now(), nil, nil); dbErr != nil {
				slog.Error("watcher: failed to mark orphaned position closed in DB", "posID", pos.ProviderPositionID, "err", dbErr)
			}
		}
		return
	}

	b.pendingCloseReasons[pos.ProviderPositionID] = pendingClose{reason: reason, sentAt: time.Now()}
}
