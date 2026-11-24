package trendpullback

import (
	"math"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

// Backtested as tfpb_0.75_1.5_30 (research/studies/2026-11-24-gold-10y-grid): with a strong H1 trend, wait for a
// pullback of 0.75-1.5 H1 ATR off the recent extreme and enter when M5 turns back with the trend.
const (
	slATRMult       = 1.5
	tpATRMult       = 2.5
	minH1ADX        = 30
	minBounceATR    = 0.75
	maxBounceATR    = 1.5
	minEMAGapRatio  = 0.6
	maxFreshStreak  = 2
	minTrackedBars  = maxFreshStreak + 1
	MaxStopDistance = 25.0 // price units ($ per 1 oz): skip setups whose stop would risk more than ~30% of the account
)

type TrendPullback struct {
	noGap bool

	lastM5Regime  string
	lastM5BarTime int64
	streak        int
	seen          int
}

// New is the live version; NewNoGap skips the H1 EMA-gap check (shadow comparison).
func New() *TrendPullback      { return &TrendPullback{} }
func NewNoGap() *TrendPullback { return &TrendPullback{noGap: true} }

func (s *TrendPullback) Name() string {
	if s.noGap {
		return "trend_pullback_nogap"
	}
	return "trend_pullback"
}

func (s *TrendPullback) UsesTrendWatcher() bool { return true }

func (s *TrendPullback) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
	hold := func(why string) strategy.EntryResult {
		return strategy.EntryResult{Signal: config.SignalHold, Reason: why}
	}
	h1, m5, m15 := states[config.PeriodH1], states[config.PeriodM5], states[config.PeriodM15]
	if !h1.IsWarmedUp || !m5.IsWarmedUp || !m15.IsWarmedUp || h1.ATR <= 0 || m15.ATR <= 0 {
		return hold("indicators not warmed up")
	}
	if h1.Regime == config.Ranging {
		return hold("H1 regime is ranging")
	}
	var dir string
	switch h1.Regime {
	case config.TrendingUp:
		dir = config.SignalBuy
	case config.TrendingDown:
		dir = config.SignalSell
	case config.Breakout:
		dir = config.SignalSell
		if m5.EMAFast > m5.EMASlow {
			dir = config.SignalBuy
		}
	default:
		return hold("H1 regime unclear")
	}

	if m5.BarTime != s.lastM5BarTime {
		if m5.Regime == s.lastM5Regime {
			s.streak++
		} else {
			s.lastM5Regime, s.streak = m5.Regime, 1
		}
		s.lastM5BarTime = m5.BarTime
		s.seen = min(s.seen+1, minTrackedBars)
	}

	buy := dir == config.SignalBuy
	want := config.TrendingDown
	if buy {
		want = config.TrendingUp
	}
	switch {
	case h1.EMA50 == 0:
		return hold("EMA50 not ready yet")
	case m5.Regime != want:
		return hold("M5 regime not turned back with the H1 trend")
	case s.seen < minTrackedBars:
		return hold("M5 regime freshness not yet trustworthy after startup")
	case s.streak > maxFreshStreak:
		return hold("M5 turn is stale, already running 3+ bars")
	case h1.ADX < minH1ADX:
		return hold("H1 ADX too low for a strong trend")
	case buy && (m5.EMAFast < m5.EMASlow || h1.EMASlow <= h1.EMA50):
		return hold("EMAs not aligned with the H1 uptrend")
	case !buy && (m5.EMAFast > m5.EMASlow || h1.EMASlow >= h1.EMA50):
		return hold("EMAs not aligned with the H1 downtrend")
	}

	gap, peak, bounce := h1.EMAFast-h1.EMASlow, h1.RecentEMAGapMax, h1.RecentHigh-price
	if !buy {
		gap, peak, bounce = -gap, -h1.RecentEMAGapMin, price-h1.RecentLow
	}
	if !s.noGap && (peak <= 0 || gap < minEMAGapRatio*peak) {
		return hold("H1 EMA9/EMA21 gap shrinking, trend fading")
	}
	pullback := bounce / h1.ATR
	if pullback < minBounceATR || pullback > maxBounceATR {
		return hold("pullback off the recent H1 extreme outside 0.75-1.5 ATR")
	}

	atr := m15.ATR
	if slATRMult*atr > MaxStopDistance {
		return hold("stop would be wider than the per-trade risk cap")
	}
	r := strategy.EntryResult{Signal: dir, ATR: atr, Confluence: 1, Tier: config.TierNormal}
	if buy {
		r.SLPrice, r.TPPrice = price-slATRMult*atr, price+tpATRMult*atr
	} else {
		r.SLPrice, r.TPPrice = price+slATRMult*atr, price-tpATRMult*atr
	}
	r.SLPips, r.TPPips = math.Abs(price-r.SLPrice)/pipSize, math.Abs(r.TPPrice-price)/pipSize
	r.Rules = map[string]strategy.RuleCheck{
		"h1_adx": {Value: round4(h1.ADX), Limit: minH1ADX,
			Why: "H1 ADX must be >= limit: strong trend"},
		"h1_pullback_atr": {Value: round4(pullback), Limit: maxBounceATR,
			Why: "H1 ATRs off the recent 6-bar extreme; must be between 0.75 and limit (a real pullback, not a crash)"},
		"m5_regime_streak": {Value: float64(s.streak), Limit: maxFreshStreak,
			Why: "M5 bars since M5 turned back with the trend; must be <= limit"},
		"stop_distance": {Value: round4(slATRMult * atr), Limit: MaxStopDistance,
			Why: "stop distance in $ per 1 oz; must be <= limit (risk cap)"},
	}
	return r
}

func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }
