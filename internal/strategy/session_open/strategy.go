package sessionopen

import (
	"math"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

const (
	slATRMult                  = 0.5
	tpRangeMult                = 1.5
	minRangePips               = 5.0
	consecutiveFailsToCooldown = 2
	cooldownDuration           = 30 * time.Minute
)

type SessionOpen struct {
	buyFailStreak  int
	sellFailStreak int
	cooldownDir    string
	cooldownUntil  time.Time
}

func New() *SessionOpen { return &SessionOpen{} }

func (s *SessionOpen) Name() string           { return "session_open" }
func (s *SessionOpen) UsesTrendWatcher() bool { return true }

// OnClosed implements strategy.OutcomeAware.
func (s *SessionOpen) OnClosed(side, closeReason string, closeTime time.Time) {
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

func (s *SessionOpen) Evaluate(states map[string]indicator.MarketState, currentPrice float64, pipSize float64) strategy.EntryResult {
	hold := func(reason string) strategy.EntryResult {
		return strategy.EntryResult{Signal: config.SignalHold, Reason: reason}
	}

	m15, ok := states[config.PeriodM15]
	if !ok || !m15.IsWarmedUp {
		return hold("M15 not warmed up")
	}

	if m15.SessionHigh == 0 || m15.SessionLow == 0 {
		return hold("not in session open window")
	}

	rangeSize := m15.SessionHigh - m15.SessionLow
	if rangeSize/pipSize < minRangePips {
		return hold("pre-session range too narrow")
	}

	if m15.ATR <= 0 {
		return hold("M15 ATR not ready")
	}

	var dir string
	switch {
	case currentPrice > m15.SessionHigh:
		dir = config.SignalBuy
	case currentPrice < m15.SessionLow:
		dir = config.SignalSell
	default:
		return hold("price inside pre-session range — no breakout")
	}

	if dir == s.cooldownDir && !s.cooldownUntil.IsZero() {
		m15Now := time.Unix(m15.BarTime, 0)
		if m15Now.Before(s.cooldownUntil) {
			return hold("cooling down after repeated same-direction invalidation")
		}
	}

	if h1, ok := states[config.PeriodH1]; ok && h1.IsWarmedUp {
		if dir == config.SignalBuy && h1.EMA50 > 0 && currentPrice < h1.EMA50 {
			return hold("BUY blocked — H1 below EMA50")
		}
		if dir == config.SignalSell && h1.EMA50 > 0 && currentPrice > h1.EMA50 {
			return hold("SELL blocked — H1 above EMA50")
		}
	}

	// Require M5 to confirm the breakout direction. A breakout with M5 regime or EMA
	// against the direction is a fakeout — the same pattern that immediately triggers
	// the watcher's ema_cross_against + regime_against signals and forces an early close.
	m5, ok := states[config.PeriodM5]
	if !ok || !m5.IsWarmedUp {
		return hold("M5 not warmed up")
	}
	if dir == config.SignalBuy && m5.Regime != config.TrendingUp {
		return hold("M5 regime not confirming BUY breakout")
	}
	if dir == config.SignalSell && m5.Regime != config.TrendingDown {
		return hold("M5 regime not confirming SELL breakout")
	}
	if dir == config.SignalBuy && m5.EMAFast < m5.EMASlow {
		return hold("M5 EMA bearish — BUY breakout not confirmed")
	}
	if dir == config.SignalSell && m5.EMAFast > m5.EMASlow {
		return hold("M5 EMA bullish — SELL breakout not confirmed")
	}
	if dir == config.SignalBuy && m5.RSI < 50 {
		return hold("M5 RSI below 50 — BUY breakout momentum weak")
	}
	if dir == config.SignalSell && m5.RSI > 50 {
		return hold("M5 RSI above 50 — SELL breakout momentum weak")
	}

	atr := m15.ATR
	var slPrice, tpPrice float64
	if dir == config.SignalBuy {
		slPrice = m15.SessionLow - slATRMult*atr
		tpPrice = currentPrice + rangeSize*tpRangeMult
	} else {
		slPrice = m15.SessionHigh + slATRMult*atr
		tpPrice = currentPrice - rangeSize*tpRangeMult
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
