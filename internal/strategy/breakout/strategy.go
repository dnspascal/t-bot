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

	maxBreakoutRejectWick = 0.4 // share of the breakout bar's range given back against the break
	minM15ADX             = 20.0
)

type Breakout struct {
	lastBreakoutBarTime int64

	pendingLevel   float64
	pendingDir     string
	pendingBarTime int64
	pendingWick    float64

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

	if m15.RangeHigh == 0 || m15.RangeLow == 0 {
		return hold("M15 range not ready")
	}

	if m15.ADX > 35 {
		return hold("ADX too high — market already trending, not a range breakout")
	}

	if m15.BarTime == s.lastBreakoutBarTime {
		return hold("breakout already signaled this M15 bar")
	}

	var dir string
	var level float64
	switch {
	case m15.Close > m15.RangeHigh:
		dir, level = config.SignalBuy, m15.RangeHigh
	case m15.Close < m15.RangeLow:
		dir, level = config.SignalSell, m15.RangeLow
	default:
		return hold("no close beyond the M15 range — failed breaks are traded by sweep")
	}

	wick := rejectWick(m15, dir)
	if wick >= maxBreakoutRejectWick {
		return hold("breakout bar rejected — closed well back from its extreme")
	}

	s.pendingWick = wick
	s.pendingDir = dir
	s.pendingLevel = level
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
	if m15.ADX < minM15ADX {
		return hold("M15 ADX too low — breakout has no momentum behind it")
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

	s.lastBreakoutBarTime = m15.BarTime

	return strategy.EntryResult{
		Rules: map[string]strategy.RuleCheck{
			"m15_breakout_reject_wick": {Value: math.Round(s.pendingWick*1e4) / 1e4, Limit: maxBreakoutRejectWick,
				Why: "breakout M15 bar: (high-close)/(high-low) for BUY, (close-low)/(high-low) for SELL; must be < limit, higher means the break was rejected (fakeout) (added 2026-11-12)"},
			"m15_adx": {Value: math.Round(m15.ADX*1e4) / 1e4, Limit: minM15ADX,
				Why: "M15 ADX on the confirmation bar; must be >= limit (and <= 35): a breakout needs momentum (added 2026-11-12)"},
			"breakout_level": {Value: level, Limit: 0, Why: "the M15 range high (BUY) or low (SELL) over the previous indicator.RangeBars bars that was broken"},
		},
		Signal:  dir,
		SLPrice: slPrice,
		TPPrice: tpPrice,
		SLPips:  slPips,
		TPPips:  tpPips,
		ATR:     atr,
		Tier:    config.TierNormal,
	}
}

func rejectWick(b indicator.MarketState, dir string) float64 {
	rng := b.High - b.Low
	if rng <= 0 {
		return 0
	}
	if dir == config.SignalBuy {
		return (b.High - b.Close) / rng
	}
	return (b.Close - b.Low) / rng
}
