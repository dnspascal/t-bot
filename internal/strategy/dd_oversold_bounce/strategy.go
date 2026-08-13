package ddoversoldbounce

import (
	"math"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

// Data-driven strategy derived from 55-day signal analysis (v2_condition_analysis_2026-08-04).
// Top condition group: H1=trending_down, M5=trending_down|breakout, M5 EMA bear,
// M5 RSI low (<50), M5 ADX trending (≥25) → 70.6% BUY win at 4h (n=34).
//
// Thesis: a strong directional move (ADX≥25) into oversold territory (RSI<50, bear EMA)
// within a macro downtrend creates a predictable snap-back. The watcher exits early if
// price continues down, keeping losses small (SL = 0.5×ATR).

const (
	adxTrending = 25.0
	tpATRMult   = 1.5
	slATRMult   = 1.0
)

type DDOversoldBounce struct{}

func New() *DDOversoldBounce { return &DDOversoldBounce{} }

func (s *DDOversoldBounce) Name() string           { return "dd_oversold_bounce" }
func (s *DDOversoldBounce) UsesTrendWatcher() bool { return true }

func (s *DDOversoldBounce) Evaluate(states map[string]indicator.MarketState, currentPrice, pipSize float64) strategy.EntryResult {
	hold := func(reason string) strategy.EntryResult {
		return strategy.EntryResult{Signal: config.SignalHold, Reason: reason}
	}

	h1, ok := states[config.PeriodH1]
	if !ok || !h1.IsWarmedUp {
		return hold("H1 not warmed up")
	}
	if h1.Regime != config.TrendingDown {
		return hold("H1 not trending down")
	}

	m5, ok := states[config.PeriodM5]
	if !ok || !m5.IsWarmedUp {
		return hold("M5 not warmed up")
	}
	if m5.Regime != config.TrendingDown && m5.Regime != config.Breakout {
		return hold("M5 regime not trending_down or breakout")
	}
	if m5.EMAFast >= m5.EMASlow {
		return hold("M5 EMA not bearish")
	}
	if m5.RSI >= 50 {
		return hold("M5 RSI not low (<50)")
	}
	if m5.ADX < adxTrending {
		return hold("M5 ADX weak — move not strong enough to bounce from")
	}
	if m5.ATR <= 0 {
		return hold("M5 ATR not ready")
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
