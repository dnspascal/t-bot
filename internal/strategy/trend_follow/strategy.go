package trendfollow

import (
	"math"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

const (
	slATRMult                  = 1.5
	tpATRMult                  = 2.5
	consecutiveFailsToCooldown = 2
	cooldownDuration           = 60 * time.Minute
)

type TrendFollow struct {
	buyFailStreak  int
	sellFailStreak int
	cooldownDir    string
	cooldownUntil  time.Time
}

func New() *TrendFollow { return &TrendFollow{} }

func (s *TrendFollow) Name() string           { return "trend_follow" }
func (s *TrendFollow) UsesTrendWatcher() bool { return true }

// OnClosed implements strategy.OutcomeAware.
func (s *TrendFollow) OnClosed(side, closeReason string, closeTime time.Time) {
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

func (s *TrendFollow) Evaluate(states map[string]indicator.MarketState, currentPrice, pipSize float64) strategy.EntryResult {

	hold := func(rsn string) strategy.EntryResult {
		return strategy.EntryResult{Signal: config.SignalHold, Reason: rsn}
	}

	h1, ok := states[config.PeriodH1]
	if !ok || !h1.IsWarmedUp {
		return hold("H1 not warmed up")
	}

	if h1.Regime == config.Ranging {
		return hold("H1 regime is ranging -- sr_bounce handles this")
	}

	var dir string

	switch h1.Regime {
	case config.TrendingUp:
		dir = config.SignalBuy
	case config.TrendingDown:
		dir = config.SignalSell
	default:
		return hold("H1 regime unclear")
	}

	m5, ok := states[config.PeriodM5]
	if !ok || !m5.IsWarmedUp {
		return hold("M5 not warmed up")
	}

	if dir == config.SignalBuy && m5.EMAFast < m5.EMASlow {
		return hold("M5 EMA bearish — not aligned with H1 uptrend")
	}

	if dir == config.SignalSell && m5.EMAFast > m5.EMASlow {
		return hold("M5 EMA bullish — not aligned with H1 downtrend")
	}

	if dir == config.SignalBuy && m5.Regime == config.TrendingDown {
		return hold("M5 actively trending down — against the H1 uptrend")
	}

	if dir == config.SignalSell && m5.Regime == config.TrendingUp {
		return hold("M5 actively trending up — against the H1 downtrend")
	}

	if h1.ADX < 20 {
		return hold("H1 ADX too low — trend too weak to chase")
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

	return strategy.EntryResult{
		Signal:     dir,
		Confluence: 1,
		Tier:       config.TierNormal,
		SLPrice:    slPrice,
		TPPrice:    tpPrice,
		SLPips:     slPips,
		TPPips:     tpPips,
		ATR:        atr,
	}

}
