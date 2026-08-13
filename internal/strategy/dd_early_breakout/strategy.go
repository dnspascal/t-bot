package ddearlybreakout

import (
	"math"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

// Data-driven strategy derived from v3_condition_analysis_2026-08-13 — an M1-granularity
// backtest that fixes v2_condition_analysis_2026-08-04's same-bar blind spot (see that
// tool's package comment; v2 is what produced dd_ranging_breakout / dd_oversold_bounce
// and missed the fast SL hits that turned up live).
//
// Condition group: H1=ranging, M15=ranging, M5=breakout, M5 EMA bear, M5 RSI mid (50-59),
// M5 ADX weak (<25), M5 momentum stable → 85.3% BUY win at 4h (n=34). Notably the win
// rate is identical across SL widths 0.5x/1.0x/1.5x ATR tested — the SL is essentially
// never touched in this condition group, unlike dd_ranging_breakout.
//
// Thesis: EMA cross is a lagging indicator. A bearish EMA cross combined with an M5
// breakout regime, neutral RSI, and weak ADX reads as price already breaking upward
// before the lagging EMA has caught up — an early-entry signal, not a contrarian one.
//
// n=34 is right at the analysis tool's minimum sample threshold — real signal, but a
// small sample. SL kept at 1.0x ATR rather than the tightest width tested: the backtest
// shows it costs nothing (SL isn't hit either way in the data) while hedging against the
// sample being smaller than it looks.
const (
	tpATRMult = 1.5
	slATRMult = 1.0
)

type DDEarlyBreakout struct{}

func New() *DDEarlyBreakout { return &DDEarlyBreakout{} }

func (s *DDEarlyBreakout) Name() string          { return "dd_early_breakout" }
func (s *DDEarlyBreakout) UsesTrendWatcher() bool { return true }

func (s *DDEarlyBreakout) Evaluate(states map[string]indicator.MarketState, currentPrice, pipSize float64) strategy.EntryResult {
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

	m15, ok := states[config.PeriodM15]
	if !ok || !m15.IsWarmedUp {
		return hold("M15 not warmed up")
	}
	if m15.Regime != config.Ranging {
		return hold("M15 not ranging")
	}

	m5, ok := states[config.PeriodM5]
	if !ok || !m5.IsWarmedUp {
		return hold("M5 not warmed up")
	}
	if m5.Regime != config.Breakout {
		return hold("M5 not in breakout")
	}
	if m5.EMAFast > m5.EMASlow {
		return hold("M5 EMA not bearish — condition requires a lagging bear cross")
	}
	if m5.RSI < 50 || m5.RSI >= 60 {
		return hold("M5 RSI outside mid band (50-59)")
	}
	if m5.ADX >= 25 {
		return hold("M5 ADX not weak — condition requires ADX < 25")
	}
	if m5.MomentumDirection != config.MomentumStable {
		return hold("M5 momentum not stable")
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
