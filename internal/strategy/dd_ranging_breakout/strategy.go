package ddrangingbreakout

import (
	"math"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

// Data-driven strategy derived from 55-day signal analysis (v2_condition_analysis_2026-08-04).
// Condition group: H1=ranging, M5=breakout, M5 EMA bull, M5 RSI mid-high (≥50)
// → 60–64% BUY win at 4h (n=59–71).
//
// Thesis: when H1 is ranging (no macro headwind), a confirmed M5 breakout with bullish
// EMA and RSI momentum has clear room to run. TP=1.5×ATR, SL=0.5×ATR matches analysis.

const (
	tpATRMult                  = 1.5
	slATRMult                  = 1.0
	consecutiveFailsToCooldown = 2
	cooldownDuration           = 60 * time.Minute
)

type DDRangingBreakout struct {
	failStreak    int
	cooldownUntil time.Time
}

func New() *DDRangingBreakout { return &DDRangingBreakout{} }

func (s *DDRangingBreakout) Name() string           { return "dd_ranging_breakout" }
func (s *DDRangingBreakout) UsesTrendWatcher() bool { return true }

// OnClosed implements strategy.OutcomeAware. This strategy only ever signals BUY.
func (s *DDRangingBreakout) OnClosed(side, closeReason string, closeTime time.Time) {
	switch strategy.ClassifyCloseReason(closeReason) {
	case strategy.CloseInvalidated:
		s.failStreak++
		if s.failStreak >= consecutiveFailsToCooldown {
			s.cooldownUntil = closeTime.Add(cooldownDuration)
		}
	case strategy.CloseValidated:
		s.failStreak = 0
	case strategy.CloseNeutral:
		// Doesn't say anything about the setup either way — leave the streak alone.
	}
}

func (s *DDRangingBreakout) Evaluate(states map[string]indicator.MarketState, currentPrice, pipSize float64) strategy.EntryResult {
	hold := func(reason string) strategy.EntryResult {
		return strategy.EntryResult{Signal: config.SignalHold, Reason: reason}
	}

	h1, ok := states[config.PeriodH1]
	if !ok || !h1.IsWarmedUp {
		return hold("H1 not warmed up")
	}
	if h1.Regime != config.Ranging {
		return hold("H1 not ranging")
	}

	m5, ok := states[config.PeriodM5]
	if !ok || !m5.IsWarmedUp {
		return hold("M5 not warmed up")
	}

	if !s.cooldownUntil.IsZero() {
		now := time.Unix(m5.BarTime, 0)
		if now.Before(s.cooldownUntil) {
			return hold("cooling down after repeated invalidation")
		}
	}
	if m5.Regime != config.Breakout {
		return hold("M5 not in breakout")
	}
	if m5.EMAFast <= m5.EMASlow {
		return hold("M5 EMA not bullish")
	}
	if m5.RSI < 50 {
		return hold("M5 RSI below 50 — insufficient momentum")
	}
	if m5.ATR <= 0 {
		return hold("M5 ATR not ready")
	}

	m15, ok := states[config.PeriodM15]
	if !ok || !m15.IsWarmedUp {
		return hold("M15 not warmed up")
	}
	if m15.ADX < 20 {
		return hold("M15 ADX too low — breakout has no real momentum")
	}

	slPrice := currentPrice - slATRMult*m5.ATR
	tpPrice := currentPrice + tpATRMult*m5.ATR
	slPips := math.Abs(currentPrice-slPrice) / pipSize
	tpPips := math.Abs(tpPrice-currentPrice) / pipSize

	if slPips < 3 || tpPips < 3 {
		return hold("SL/TP too tight")
	}

	return strategy.EntryResult{
		Signal:  config.SignalBuy,
		SLPrice: slPrice,
		TPPrice: tpPrice,
		SLPips:  slPips,
		TPPips:  tpPips,
		ATR:     m5.ATR,
		Tier:    config.TierNormal,
	}
}
