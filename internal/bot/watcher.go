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

// Active — see analysis/daily/2026-09-02/queries/peak_retention_sim.
const peakRetentionPct = 25.0

// Old two-gate design, superseded by peakRetentionPct above — kept for reference.
const peakDrawbackThreshold = 60.0
const peakDrawbackGatePct = 70.0

const neverProfitableTimeout = 30 * time.Minute

const stallTimeout = 60 * time.Minute
const stallMaxFavPct = 15.0

const signalsToClose = 3

const signalsToReduce = 2

// Raised from 25.0 — see analysis/daily/2026-08-26/report.html Part 8.
const breakEvenTriggerPct = 50.0
const breakEvenBufferPipsDefault = 2.0

var breakEvenBufferPipsOverride = map[string]float64{
	"XAUUSD": 5.0,
}

func (b *Bot) breakEvenBufferPips() float64 {
	if v, ok := breakEvenBufferPipsOverride[b.symbol]; ok {
		return v
	}
	return breakEvenBufferPipsDefault
}

func (b *Bot) decisionParams() map[string]any {
	return map[string]any{
		"peak_retention_arm_pct":        breakEvenTriggerPct,
		"peak_retention_pct":            peakRetentionPct,
		"breakeven_trigger_pct":         breakEvenTriggerPct,
		"breakeven_buffer_pips":         b.breakEvenBufferPips(),
		"signals_to_close":              signalsToClose,
		"signals_to_reduce":             signalsToReduce,
		"never_profitable_timeout_min":  int(neverProfitableTimeout / time.Minute),
		"stall_timeout_min":             int(stallTimeout / time.Minute),
		"stall_max_favorable_pct_of_tp": stallMaxFavPct,
	}
}

var continuousTradingSymbols = map[string]bool{
	"XRPUSD": true,
}

func (b *Bot) tradesContinuously() bool {
	return continuousTradingSymbols[b.symbol]
}

func (b *Bot) isEODWindow() bool {
	if b.tradesContinuously() {
		return false
	}
	now := time.Now().UTC()
	wd := now.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return false
	}
	return now.Hour() == 21 && now.Minute() >= 30
}

func (b *Bot) watchPositions(ctx context.Context, ms indicator.MarketState) {
	if b.isEODWindow() {
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
			n, signals := countReversalSignals(ms, pos)
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

func countReversalSignals(ms indicator.MarketState, pos trackedPosition) (int, []string) {
	var signals []string

	if pct := peakGivebackPct(pos, ms.Close); pct > 0 {
		signals = append(signals, fmt.Sprintf(strat.PeakDrawbackPrefix+"%.0f%%", pct))
	}

	if (pos.Side == config.SignalBuy && (ms.Regime == config.TrendingDown || ms.Regime == config.Ranging)) ||
		(pos.Side == config.SignalSell && (ms.Regime == config.TrendingUp || ms.Regime == config.Ranging)) {
		signals = append(signals, strat.ReversalRegimeAgainst)
	}

	if (pos.Side == config.SignalBuy && ms.RSI < rsiMidline) ||
		(pos.Side == config.SignalSell && ms.RSI > rsiMidline) {
		signals = append(signals, strat.ReversalRSIAgainst)
	}

	if (pos.Side == config.SignalBuy && ms.EMAFast < ms.EMASlow) ||
		(pos.Side == config.SignalSell && ms.EMAFast > ms.EMASlow) {
		signals = append(signals, strat.ReversalEMACrossAgainst)
	}

	if (pos.Side == config.SignalBuy && ms.MomentumDirection == config.MomentumFalling) ||
		(pos.Side == config.SignalSell && ms.MomentumDirection == config.MomentumRising) {
		signals = append(signals, strat.ReversalMomentumAgainst)
	}

	return len(signals), signals
}

func peakGivebackPct(pos trackedPosition, currentPrice float64) float64 {
	if pos.OpenPrice == 0 {
		return 0
	}

	var tpDist float64
	if pos.Side == config.SignalBuy {
		tpDist = pos.TPPrice - pos.OpenPrice
	} else {
		tpDist = pos.OpenPrice - pos.TPPrice
	}
	if tpDist <= 0 {
		return 0
	}
	armGain := tpDist * (breakEvenTriggerPct / 100)

	var peakGain, currentGain float64
	if pos.Side == config.SignalBuy {
		peakGain = pos.MaxFavorable - pos.OpenPrice
		currentGain = currentPrice - pos.OpenPrice
	} else {
		peakGain = pos.OpenPrice - pos.MaxFavorable
		currentGain = pos.OpenPrice - currentPrice
	}

	if peakGain < armGain || peakGain <= 0 {
		return 0
	}

	floor := peakGain * (peakRetentionPct / 100)
	if currentGain > floor {
		return 0
	}

	return ((peakGain - currentGain) / peakGain) * 100
}

func peakDrawbackPct(pos trackedPosition, currentPrice float64) float64 {
	if pos.OpenPrice == 0 {
		return 0
	}

	var tpDist float64
	if pos.Side == config.SignalBuy {
		tpDist = pos.TPPrice - pos.OpenPrice
	} else {
		tpDist = pos.OpenPrice - pos.TPPrice
	}
	if tpDist <= 0 {
		return 0
	}
	minPeakGain := tpDist * (peakDrawbackGatePct / 100)

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

	return (gaveBack / tpDist) * 100
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

		if pct := peakGivebackPct(pos, currentPrice); pct > 0 {
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

	bufferPips := b.breakEvenBufferPips()
	var newSL float64
	if pos.Side == config.SignalBuy {
		newSL = pos.OpenPrice + bufferPips*b.pipSize
	} else {
		newSL = pos.OpenPrice - bufferPips*b.pipSize
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

	go b.awaitBreakEvenConfirmation(ctx, pos, newSL, attempt, confirmCh)
}

func (b *Bot) awaitBreakEvenConfirmation(ctx context.Context, pos trackedPosition, newSL float64, attempt int, confirmCh chan struct{}) {
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

	bufferPips := b.breakEvenBufferPips()
	var newSL, newTP float64
	if pos.Side == config.SignalBuy {
		newSL = pos.SLPrice - bufferPips*b.pipSize
		newTP = pos.TPPrice + bufferPips*b.pipSize
	} else {
		newSL = pos.SLPrice + bufferPips*b.pipSize
		newTP = pos.TPPrice - bufferPips*b.pipSize
	}

	confirmed := false
	for attempt := 1; attempt <= breakEvenMaxAttempts; attempt++ {
		if err := b.provider.AmendPositionSL(ctx, pos.ProviderPositionID, newSL, newTP); err != nil {
			slog.Error("TESTAMEND: AmendPositionSL send FAILED",
				"posID", pos.ProviderPositionID, "newSL", newSL, "newTP", newTP, "attempt", attempt, "err", err,
			)
			break
		}

		confirmCh := make(chan struct{})
		b.pendingBreakEvenMu.Lock()
		b.pendingBreakEven[pos.ProviderPositionID] = confirmCh
		b.pendingBreakEvenMu.Unlock()

		slog.Info("TESTAMEND: amend SENT — awaiting broker confirmation",
			"posID", pos.ProviderPositionID, "openPrice", pos.OpenPrice, "newSL", newSL, "newTP", newTP,
			"attempt", attempt, "maxAttempts", breakEvenMaxAttempts,
		)

		select {
		case <-confirmCh:
			slog.Info("TESTAMEND: CONFIRMED by broker", "posID", pos.ProviderPositionID, "newSL", newSL, "newTP", newTP, "attempt", attempt)
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
		if _, pending := b.pendingCloseReasons[pos.ProviderPositionID]; pending {
			continue
		}

		if time.Since(pos.OpenTime) >= neverProfitableTimeout {
			neverProfitable := false
			if pos.Side == config.SignalBuy && pos.MaxFavorable <= pos.OpenPrice+b.pipSize {
				neverProfitable = true
			} else if pos.Side == config.SignalSell && pos.MaxFavorable >= pos.OpenPrice-b.pipSize {
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
				continue
			}
		}

		if time.Since(pos.OpenTime) >= stallTimeout {
			var tpDist, peakGain float64
			if pos.Side == config.SignalBuy {
				tpDist = pos.TPPrice - pos.OpenPrice
				peakGain = pos.MaxFavorable - pos.OpenPrice
			} else {
				tpDist = pos.OpenPrice - pos.TPPrice
				peakGain = pos.OpenPrice - pos.MaxFavorable
			}
			if tpDist <= 0 {
				continue
			}
			maxFavPct := (peakGain / tpDist) * 100
			if maxFavPct < stallMaxFavPct {
				slog.Info("time stop — stalled, never made meaningful progress toward TP",
					"posID", pos.ProviderPositionID,
					"side", pos.Side,
					"maxFavorablePctOfTP", fmt.Sprintf("%.1f%%", maxFavPct),
					"duration", time.Since(pos.OpenTime).Round(time.Minute),
				)
				b.closeTrackedPosition(ctx, pos, strat.CloseReasonTimeStopStalled60m)
			}
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
		if pos.Side == config.SignalBuy {
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
