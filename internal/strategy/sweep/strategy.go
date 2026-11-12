// Package sweep trades failed M15 range breaks (liquidity sweeps) in the H1 trend direction.
package sweep

import (
	"math"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

const (
	slATRMult = 1.0
	tpATRMult = 2.0

	maxRejectWick = 0.4  // sweep bar must close in the far 40% of its range, away from the swept side
	minM15ADX     = 20.0 // momentum needed on the confirmation bar
	maxM15ADX     = 35.0 // above this the market is already running, not ranging

	consecutiveFailsToCooldown = 2
	cooldownDuration           = 30 * time.Minute
)

type setup struct {
	dir     string
	level   float64
	depth   float64 // how far the sweep bar went beyond the level
	wick    float64
	barTime int64
}

type Sweep struct {
	lastBarTime int64
	pending     *setup

	buyFailStreak  int
	sellFailStreak int
	cooldownDir    string
	cooldownUntil  time.Time
}

func New() *Sweep { return &Sweep{} }

func (s *Sweep) Name() string           { return "sweep" }
func (s *Sweep) UsesTrendWatcher() bool { return true }

// OnClosed implements strategy.OutcomeAware.
func (s *Sweep) OnClosed(side, closeReason string, closeTime time.Time) {
	switch strategy.ClassifyCloseReason(closeReason) {
	case strategy.CloseInvalidated:
		streak := &s.sellFailStreak
		if side == config.SignalBuy {
			streak = &s.buyFailStreak
		}
		*streak++
		if *streak >= consecutiveFailsToCooldown {
			s.cooldownDir = side
			s.cooldownUntil = closeTime.Add(cooldownDuration)
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
	}
}

func (s *Sweep) Evaluate(states map[string]indicator.MarketState, currentPrice, pipSize float64) strategy.EntryResult {
	hold := func(rsn string) strategy.EntryResult {
		return strategy.EntryResult{Signal: config.SignalHold, Reason: rsn}
	}

	m15, ok := states[config.PeriodM15]
	if !ok || !m15.IsWarmedUp || m15.ATR <= 0 {
		return hold("M15 not warmed up")
	}
	h1, ok := states[config.PeriodH1]
	if !ok || !h1.IsWarmedUp {
		return hold("H1 not warmed up")
	}
	if m15.BarTime == s.lastBarTime {
		return hold("waiting for the next M15 bar")
	}
	s.lastBarTime = m15.BarTime

	if p := s.pending; p != nil {
		s.pending = nil
		return s.confirm(p, m15, h1, currentPrice, pipSize)
	}

	if m15.RangeHigh == 0 || m15.RangeLow == 0 {
		return hold("M15 range not ready")
	}
	if m15.ADX > maxM15ADX {
		return hold("M15 ADX too high — already trending, not a range")
	}
	rng := m15.High - m15.Low
	if rng <= 0 {
		return hold("M15 bar has no range")
	}

	switch {
	case m15.High > m15.RangeHigh && m15.Close < m15.RangeHigh:
		wick := (m15.Close - m15.Low) / rng
		if wick >= maxRejectWick {
			return hold("range high swept but bar closed weakly — no clear rejection")
		}
		s.pending = &setup{config.SignalSell, m15.RangeHigh, m15.High - m15.RangeHigh, wick, m15.BarTime}
		return hold("range high swept — awaiting next-bar confirmation")
	case m15.Low < m15.RangeLow && m15.Close > m15.RangeLow:
		wick := (m15.High - m15.Close) / rng
		if wick >= maxRejectWick {
			return hold("range low swept but bar closed weakly — no clear rejection")
		}
		s.pending = &setup{config.SignalBuy, m15.RangeLow, m15.RangeLow - m15.Low, wick, m15.BarTime}
		return hold("range low swept — awaiting next-bar confirmation")
	}
	return hold("no sweep")
}

func (s *Sweep) confirm(p *setup, m15, h1 indicator.MarketState, currentPrice, pipSize float64) strategy.EntryResult {
	hold := func(rsn string) strategy.EntryResult {
		return strategy.EntryResult{Signal: config.SignalHold, Reason: rsn}
	}

	backInside := (p.dir == config.SignalSell && m15.Close < p.level) || (p.dir == config.SignalBuy && m15.Close > p.level)
	if !backInside {
		return hold("sweep not confirmed — next bar closed beyond the level (real breakout)")
	}
	if p.dir == config.SignalBuy && h1.Regime != config.TrendingUp {
		return hold("swept low but H1 not trending up")
	}
	if p.dir == config.SignalSell && h1.Regime != config.TrendingDown {
		return hold("swept high but H1 not trending down")
	}
	if m15.ADX < minM15ADX || m15.ADX > maxM15ADX {
		return hold("M15 ADX outside 20-35 on the confirmation bar")
	}
	if p.dir == s.cooldownDir && time.Unix(m15.BarTime, 0).Before(s.cooldownUntil) {
		return hold("cooling down after repeated same-direction invalidation")
	}

	atr := m15.ATR
	slPrice, tpPrice := currentPrice-slATRMult*atr, currentPrice+tpATRMult*atr
	if p.dir == config.SignalSell {
		slPrice, tpPrice = currentPrice+slATRMult*atr, currentPrice-tpATRMult*atr
	}
	slPips := math.Abs(currentPrice-slPrice) / pipSize
	tpPips := math.Abs(tpPrice-currentPrice) / pipSize
	if slPips < 3 || tpPips < 3 {
		return hold("SL/TP too tight")
	}

	return strategy.EntryResult{
		Signal:  p.dir,
		SLPrice: slPrice,
		TPPrice: tpPrice,
		SLPips:  slPips,
		TPPips:  tpPips,
		ATR:     atr,
		Tier:    config.TierNormal,
		Rules: map[string]strategy.RuleCheck{
			"range_level": {Value: round4(p.level), Limit: indicator.RangeBars,
				Why: "high (SELL) or low (BUY) of the previous M15 bars (limit = bar count) that was swept"},
			"sweep_depth_atr": {Value: round4(p.depth / atr), Limit: 0,
				Why: "how far the sweep bar went beyond the level, in M15 ATR"},
			"sweep_reject_wick": {Value: round4(p.wick), Limit: maxRejectWick,
				Why: "sweep bar: share of its range between the close and the far end; must be < limit, lower means a strong rejection back into the range (added 2026-11-12)"},
			"m15_adx": {Value: round4(m15.ADX), Limit: minM15ADX,
				Why: "M15 ADX on the confirmation bar; must be between limit and 35 (added 2026-11-12)"},
			"h1_adx": {Value: round4(h1.ADX), Limit: 20,
				Why: "info only, not a filter: backtest sweeps won most when H1 ADX < limit (18 trades); watching it live"},
			"confirm_inside_atr": {Value: round4(math.Abs(m15.Close-p.level) / atr), Limit: 0,
				Why: "how far back inside the range the confirmation bar closed, in M15 ATR"},
		},
	}
}

func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }
