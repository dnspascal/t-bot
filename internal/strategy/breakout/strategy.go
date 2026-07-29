package breakout

import (
	"math"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

const (
	slATRMult = 1.0
	tpATRMult = 2.0
)

type Breakout struct {
	lastBreakoutBarTime int64 
}

func New() *Breakout { return &Breakout{} }

func (s *Breakout) Name() string           { return "breakout" }
func (s *Breakout) UsesTrendWatcher() bool { return true }

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

	if h1, ok := states[config.PeriodH1]; ok && h1.IsWarmedUp {
		// Breakout must align with the H1 trend — counter-trend breakouts are fakeouts.
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
		slPrice = m15.BreakoutLevel - slATRMult*atr
		tpPrice = currentPrice + tpATRMult*atr
	} else {
		slPrice = m15.BreakoutLevel + slATRMult*atr
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
