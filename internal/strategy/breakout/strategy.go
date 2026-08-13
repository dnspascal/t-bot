package breakout

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

	consecutiveFailsToCooldown = 2
	cooldownDuration           = 30 * time.Minute
)

type Breakout struct {
	lastBreakoutBarTime int64

	pendingLevel   float64
	pendingDir     string
	pendingBarTime int64

	buyFailStreak  int
	sellFailStreak int
	cooldownDir    string
	cooldownUntil  time.Time
}

func New() *Breakout { return &Breakout{} }

func (s *Breakout) Name() string           { return "breakout" }
func (s *Breakout) UsesTrendWatcher() bool { return true }

// OnClosed implements strategy.OutcomeAware.
func (s *Breakout) OnClosed(side, closeReason string, closeTime time.Time) {
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

func (s *Breakout) Evaluate(states map[string]indicator.MarketState, currentPrice, pipSize float64) strategy.EntryResult {
	hold := func(rsn string) strategy.EntryResult {
		return strategy.EntryResult{Signal: config.SignalHold, Reason: rsn}
	}

	m15, ok := states[config.PeriodM15]
	if !ok || !m15.IsWarmedUp {
		return hold("M15 not warmed up")
	}

	if m15.ATR <= 0 {
		return hold("M15 ATR not ready")
	}

	if s.pendingDir != "" && m15.BarTime != s.pendingBarTime {
		dir := s.pendingDir
		level := s.pendingLevel
		s.pendingDir = ""

		held := (dir == config.SignalBuy && m15.Close > level) ||
			(dir == config.SignalSell && m15.Close < level)
		if !held {
			return hold("breakout not confirmed — price closed back inside the range")
		}
		return s.tryEnter(states, m15, currentPrice, pipSize, dir, level)
	}

	if m15.BreakoutLevel == 0 {
		return hold("no breakout detected")
	}

	if m15.ADX > 35 {
		return hold("ADX too high — market already trending, not a range breakout")
	}

	if m15.BarTime == s.lastBreakoutBarTime {
		return hold("breakout already signaled this M15 bar")
	}

	var dir string
	switch {
	case m15.Close > m15.BreakoutLevel:
		dir = config.SignalBuy
	case m15.Close < m15.BreakoutLevel:
		dir = config.SignalSell
	default:
		return hold("M15 closed exactly at breakout level")
	}

	s.pendingDir = dir
	s.pendingLevel = m15.BreakoutLevel
	s.pendingBarTime = m15.BarTime
	return hold("breakout detected — awaiting next-bar confirmation")
}

func (s *Breakout) tryEnter(states map[string]indicator.MarketState, m15 indicator.MarketState, currentPrice, pipSize float64, dir string, level float64) strategy.EntryResult {
	hold := func(rsn string) strategy.EntryResult {
		return strategy.EntryResult{Signal: config.SignalHold, Reason: rsn}
	}

	if dir == s.cooldownDir && !s.cooldownUntil.IsZero() {
		now := time.Unix(m15.BarTime, 0)
		if now.Before(s.cooldownUntil) {
			return hold("cooling down after repeated same-direction invalidation")
		}
	}

	if m15.ADX > 35 {
		return hold("ADX too high — market already trending, not a range breakout")
	}

	if h1, ok := states[config.PeriodH1]; ok && h1.IsWarmedUp {
		if dir == config.SignalBuy && h1.Regime != config.TrendingUp {
			return hold("BUY breakout blocked — H1 not trending up")
		}
		if dir == config.SignalSell && h1.Regime != config.TrendingDown {
			return hold("SELL breakout blocked — H1 not trending down")
		}
	}

	atr := m15.ATR
	var slPrice, tpPrice float64

	if dir == config.SignalBuy {
		slPrice = level - slATRMult*atr
		tpPrice = currentPrice + tpATRMult*atr
	} else {
		slPrice = level + slATRMult*atr
		tpPrice = currentPrice - tpATRMult*atr
	}

	slPips := math.Abs(currentPrice-slPrice) / pipSize
	tpPips := math.Abs(tpPrice-currentPrice) / pipSize

	if slPips < 3 || tpPips < 3 {
		return hold("SL/TP too tight")
	}

	s.lastBreakoutBarTime = m15.BarTime

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
