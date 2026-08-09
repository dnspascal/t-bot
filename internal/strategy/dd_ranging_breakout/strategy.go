package ddrangingbreakout

import (
	"math"

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
	tpATRMult = 1.5
	slATRMult = 0.5
)

type DDRangingBreakout struct{}

func New() *DDRangingBreakout { return &DDRangingBreakout{} }

func (s *DDRangingBreakout) Name() string           { return "dd_ranging_breakout" }
func (s *DDRangingBreakout) UsesTrendWatcher() bool { return true }

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
