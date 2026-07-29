package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
)

const peakDrawbackThreshold = 60.0
const neverProfitableTimeout = 30 * time.Minute

const signalsToClose = 3

const signalsToReduce = 2

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
			b.closeTrackedPosition(ctx, pos, "eod_close")
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

			slog.Info("reversal signals detected")

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
		signals = append(signals, fmt.Sprintf("peak_drawback=%.0f%%", pct))
	}

	if (pos.Side == "BUY" && (ms.Regime == "trending_down" || ms.Regime == "ranging")) ||
		(pos.Side == "SELL" && (ms.Regime == "trending_up" || ms.Regime == "ranging")) {
		signals = append(signals, "regime_against")
	}

	if (pos.Side == "BUY" && ms.RSI < rsiMidline) ||
		(pos.Side == "SELL" && ms.RSI > rsiMidline) {
		signals = append(signals, "rsi_against")
	}

	if (pos.Side == "BUY" && ms.EMAFast < ms.EMASlow) ||
		(pos.Side == "SELL" && ms.EMAFast > ms.EMASlow) {
		signals = append(signals, "ema_cross_against")
	}

	if (pos.Side == "BUY" && ms.MomentumDirection == "falling") ||
		(pos.Side == "SELL" && ms.MomentumDirection == "rising") {
		signals = append(signals, "momentum_against")
	}

	return len(signals), signals
}


func peakDrawbackPct(pos trackedPosition, currentPrice, pipSize float64) float64 {
	if pos.OpenPrice == 0 {
		return 0
	}

	var tpDist float64
	if pos.Side == "BUY" {
		tpDist = pos.TPPrice - pos.OpenPrice
	} else {
		tpDist = pos.OpenPrice - pos.TPPrice
	}
	minPeakGain := tpDist * 0.33
	if minPeakGain <= 0 {
		minPeakGain = 3 * pipSize 
	}

	var peakGain, currentGain float64
	if pos.Side == "BUY" {
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
		if pct := peakDrawbackPct(pos, currentPrice, b.pipSize); pct >= peakDrawbackThreshold {
			reason := fmt.Sprintf("peak_drawback=%.0f%%", pct)
			slog.Info("peak drawback — closing position",
				"posID", pos.ProviderPositionID,
				"side", pos.Side,
				"drawback", fmt.Sprintf("%.0f%%", pct),
			)
			b.closeTrackedPosition(ctx, pos, reason)
		}
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
			b.closeTrackedPosition(ctx, pos, "time_stop=never_profitable_30m")
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

		slog.Info("M1 position state",
			"provider", provider,
			"positions", count,
			"posID", pos.ProviderPositionID,
			"side", pos.Side,
			"open", pos.OpenPrice,
			"price", currentPrice,
			"pnlUSD", fmt.Sprintf("%+.4f", unrealized),
			"maxFav", pos.MaxFavorable,
			"maxAdv", pos.MaxAdverse,
			"drawbackPct", fmt.Sprintf("%.1f%%", peakDrawbackPct(pos, currentPrice, b.pipSize)),
		)
	}

	if count > 1 {
		slog.Info("M1 total P&L",
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
