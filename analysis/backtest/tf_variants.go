package main

import (
	"fmt"
	"math"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

// Research: trend_follow's checks with the bounce rule inverted — enter the second leg
// after a pullback, once M5 turns back with the H1 trend.
type tfPullback struct {
	minBounce, maxBounce float64 // H1 ATR off the 6-bar extreme
	minADX               float64
	noGap                bool

	lastM5Regime  string
	lastM5BarTime int64
	streak        int
	seen          int
}

func init() {
	for _, lo := range []float64{0.5, 0.75, 1.0} {
		for _, hi := range []float64{1.5, 2.0, 3.0} {
			for _, adx := range []float64{20, 30, 40} {
				lo, hi, adx := lo, hi, adx
				strategies[fmt.Sprintf("tfpb_%.2f_%.1f_%.0f", lo, hi, adx)] = func() strategy.Strategy {
					return &tfPullback{minBounce: lo, maxBounce: hi, minADX: adx}
				}
				strategies[fmt.Sprintf("tfpbng_%.2f_%.1f_%.0f", lo, hi, adx)] = func() strategy.Strategy {
					return &tfPullback{minBounce: lo, maxBounce: hi, minADX: adx, noGap: true}
				}
			}
		}
	}
}

func (s *tfPullback) Name() string {
	p := "tfpb"
	if s.noGap {
		p = "tfpbng"
	}
	return fmt.Sprintf("%s_%.2f_%.1f_%.0f", p, s.minBounce, s.maxBounce, s.minADX)
}
func (s *tfPullback) UsesTrendWatcher() bool { return true }

func (s *tfPullback) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
	hold := strategy.EntryResult{Signal: config.SignalHold}
	h1, m5, m15 := states[config.PeriodH1], states[config.PeriodM5], states[config.PeriodM15]
	if !h1.IsWarmedUp || !m5.IsWarmedUp || !m15.IsWarmedUp || h1.Regime == config.Ranging || h1.ATR <= 0 || m15.ATR <= 0 {
		return hold
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
		return hold
	}

	if m5.BarTime != s.lastM5BarTime {
		if m5.Regime == s.lastM5Regime {
			s.streak++
		} else {
			s.lastM5Regime, s.streak = m5.Regime, 1
		}
		s.lastM5BarTime = m5.BarTime
		s.seen = min(s.seen+1, 3)
	}

	buy := dir == config.SignalBuy
	wantRegime := config.TrendingDown
	if buy {
		wantRegime = config.TrendingUp
	}
	if h1.EMA50 == 0 || m5.Regime != wantRegime || s.seen < 3 || s.streak > 2 || h1.ADX < s.minADX {
		return hold
	}
	if buy && (m5.EMAFast < m5.EMASlow || h1.EMASlow <= h1.EMA50) || !buy && (m5.EMAFast > m5.EMASlow || h1.EMASlow >= h1.EMA50) {
		return hold
	}

	gap, peak, bounce := h1.EMAFast-h1.EMASlow, h1.RecentEMAGapMax, h1.RecentHigh-price
	if !buy {
		gap, peak, bounce = -gap, -h1.RecentEMAGapMin, price-h1.RecentLow
	}
	if !s.noGap && (peak <= 0 || gap < 0.6*peak) {
		return hold
	}
	if b := bounce / h1.ATR; b < s.minBounce || b > s.maxBounce {
		return hold
	}

	atr := m15.ATR
	r := strategy.EntryResult{Signal: dir, ATR: atr, Tier: config.TierNormal}
	if buy {
		r.SLPrice, r.TPPrice = price-1.5*atr, price+2.5*atr
	} else {
		r.SLPrice, r.TPPrice = price+1.5*atr, price-2.5*atr
	}
	r.SLPips, r.TPPips = math.Abs(price-r.SLPrice)/pipSize, math.Abs(r.TPPrice-price)/pipSize
	return r
}
