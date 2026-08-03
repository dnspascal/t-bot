package trendfollow

import (
	"math"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

type TrendFollow struct{}

const (
	slATRMult = 1.5
	tpATRMult = 2.5
)

func New() *TrendFollow { return &TrendFollow{} }

func (s *TrendFollow) Name() string           { return "trend_follow" }
func (s *TrendFollow) UsesTrendWatcher() bool { return true }

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

	if dir == config.SignalBuy && m5.Regime != config.TrendingUp {
		return hold("M5 regime not confirming H1 uptrend")
	}

	if dir == config.SignalSell && m5.Regime != config.TrendingDown {
		return hold("M5 regime not confirming H1 downtrend")
	}

	m15, ok := states[config.PeriodM15]
	if !ok || !m15.IsWarmedUp || m15.ATR <= 0 {
		return hold("M15 ATR not ready")
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
