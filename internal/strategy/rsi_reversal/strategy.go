package rsireversal

import (
	"math"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

const (
	rsiBuyThreshold  = 28.0
	rsiSellThreshold = 72.0
	slATRMult        = 1.0
	tpATRMult        = 1.5

	consecutiveFailsToCooldown = 2
	cooldownDuration           = 60 * time.Minute
)

type RSIReversal struct {
	buyFailStreak  int
	sellFailStreak int
	cooldownDir    string
	cooldownUntil  time.Time
}

func New() *RSIReversal { return &RSIReversal{} }

func (s *RSIReversal) Name() string           { return "rsi_reversal" }
func (s *RSIReversal) UsesTrendWatcher() bool { return true }

// OnClosed implements strategy.OutcomeAware.
func (s *RSIReversal) OnClosed(side, closeReason string, closeTime time.Time) {
	switch strategy.ClassifyCloseReason(closeReason) {
	case strategy.CloseInvalidated:
		if side == config.SignalBuy {
			s.buyFailStreak++
			if s.buyFailStreak >= consecutiveFailsToCooldown {
				s.cooldownDir = config.SignalBuy
				s.cooldownUntil = closeTime.Add(cooldownDuration)
			}
		} else {
			s.sellFailStreak++
			if s.sellFailStreak >= consecutiveFailsToCooldown {
				s.cooldownDir = config.SignalSell
				s.cooldownUntil = closeTime.Add(cooldownDuration)
			}
		}
	case strategy.CloseValidated:
		if side == config.SignalBuy {
			s.buyFailStreak = 0
		} else {
			s.sellFailStreak = 0
		}
		if s.cooldownDir == side {
			s.cooldownDir = ""
		}
	case strategy.CloseNeutral:
		// Doesn't say anything about the setup either way — leave the streak alone.
	}
}

func (s *RSIReversal) Evaluate(states map[string]indicator.MarketState, currentPrice, pipSize float64) strategy.EntryResult {
	hold := func(rsn string) strategy.EntryResult {
		return strategy.EntryResult{Signal: config.SignalHold, Reason: rsn}
	}

	h1, ok := states[config.PeriodH1]
	if !ok || !h1.IsWarmedUp {
		return hold("H1 not warmed up")
	}

	var dir string
	switch {
	case h1.RSI < rsiBuyThreshold:
		dir = config.SignalBuy
	case h1.RSI > rsiSellThreshold:
		dir = config.SignalSell
	default:
		return hold("H1 RSI not at extreme")
	}

	if dir == config.SignalBuy && (h1.Regime == config.TrendingDown || h1.EMAFast < h1.EMASlow) {
		return hold("H1 trending down or EMA bearish — RSI oversold may persist")
	}
	if dir == config.SignalSell && (h1.Regime == config.TrendingUp || h1.EMAFast > h1.EMASlow) {
		return hold("H1 trending up or EMA bullish — RSI overbought may persist")
	}

	m5, ok := states[config.PeriodM5]
	if !ok || !m5.IsWarmedUp {
		return hold("M5 not warmed up")
	}
	if dir == config.SignalBuy && m5.Close <= m5.Open {
		return hold("M5 not showing bullish reversal candle")
	}
	if dir == config.SignalSell && m5.Close >= m5.Open {
		return hold("M5 not showing bearish reversal candle")
	}

	if h1.EMA50 > 0 {
		if dir == config.SignalBuy && currentPrice > h1.EMA50 {
			return hold("BUY blocked — price above H1 EMA50 despite oversold RSI")
		}
		if dir == config.SignalSell && currentPrice < h1.EMA50 {
			return hold("SELL blocked — price below H1 EMA50 despite overbought RSI")
		}
	}

	m15, ok := states[config.PeriodM15]
	if !ok || !m15.IsWarmedUp || m15.ATR <= 0 {
		return hold("M15 ATR not ready")
	}

	if dir == s.cooldownDir && !s.cooldownUntil.IsZero() {
		now := time.Unix(m15.BarTime, 0)
		if now.Before(s.cooldownUntil) {
			return hold("cooling down after repeated same-direction invalidation")
		}
	}

	atr := m15.ATR
	var slPrice, tpPrice float64
	if dir == config.SignalBuy {
		slPrice = currentPrice - slATRMult*atr
		tpPrice = currentPrice + tpATRMult*atr
	} else {
		slPrice = currentPrice + slATRMult*atr
		tpPrice = currentPrice - tpATRMult*atr
	}

	slPips := math.Abs(currentPrice-slPrice) / pipSize
	tpPips := math.Abs(tpPrice-currentPrice) / pipSize

	if slPips < 3 || tpPips < 3 {
		return hold("SL/TP too tight")
	}

	return strategy.EntryResult{
		Signal:  dir,
		SLPrice: slPrice,
		TPPrice: tpPrice,
		SLPips:  slPips,
		TPPips:  tpPips,
		ATR:     atr,
		Tier:    config.TierNormal,
	}
}
