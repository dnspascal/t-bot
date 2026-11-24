package main

import (
	"fmt"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

// Research: fade false breaks of a range while the market is ranging (H1 ADX below a limit). Range sources: the last
// 12/24/48 H1 bars, the previous day, the Asian session (00-07 UTC) or London (07-12 UTC). An M5 or M15 bar that pokes
// beyond an edge and closes back inside is faded; stop k x H1 ATR beyond the poke, target the middle or the far edge.
type rangeFade struct {
	src     string // h12, h24, h48, pday, asia, london
	maxADX  float64
	tf      string // M5 or M15 confirmation bar
	k       float64
	far     bool
	minWide float64 // range must be at least this many H1 ATR wide

	lastH1, lastBar int64
	highs, lows     []float64
	day             string
	sHi, sLo        float64
	coolUntil       map[string]int64
}

func init() {
	for _, src := range []string{"h12", "h24", "h48", "pday", "asia", "london"} {
		for _, adx := range []float64{20, 25} {
			for _, tf := range []string{"M5", "M15"} {
				for _, k := range []float64{0.25, 0.5} {
					for _, far := range []bool{false, true} {
						for _, w := range []float64{1, 2} {
							src, adx, tf, k, far, w := src, adx, tf, k, far, w
							r := &rangeFade{src: src, maxADX: adx, tf: tf, k: k, far: far, minWide: w}
							strategies[r.Name()] = func() strategy.Strategy {
								return &rangeFade{src: src, maxADX: adx, tf: tf, k: k, far: far, minWide: w}
							}
						}
					}
				}
			}
		}
	}
}

func (s *rangeFade) Name() string {
	t := "mid"
	if s.far {
		t = "far"
	}
	return fmt.Sprintf("rng_%s_adx%.0f_%s_%.2f_%s_w%.0f", s.src, s.maxADX, s.tf, s.k, t, s.minWide)
}

func (s *rangeFade) UsesTrendWatcher() bool { return false }

func (s *rangeFade) rangeNow(h1, m5 indicator.MarketState) (float64, float64, bool) {
	t := time.Unix(m5.BarTime, 0).UTC()
	switch s.src {
	case "h12", "h24", "h48":
		n := map[string]int{"h12": 12, "h24": 24, "h48": 48}[s.src]
		if len(s.highs) < n {
			return 0, 0, false
		}
		hi, lo := s.highs[len(s.highs)-n], s.lows[len(s.lows)-n]
		for i := len(s.highs) - n; i < len(s.highs); i++ {
			hi, lo = max(hi, s.highs[i]), min(lo, s.lows[i])
		}
		return hi, lo, true
	case "pday":
		return h1.PrevDayHigh, h1.PrevDayLow, h1.PrevDayHigh > 0
	}
	from, to := 0, 7
	if s.src == "london" {
		from, to = 7, 12
	}
	if d := t.Format("2006-01-02"); d != s.day {
		s.day, s.sHi, s.sLo = d, 0, 0
	}
	if h := t.Hour(); h >= from && h < to {
		if s.sHi == 0 {
			s.sHi, s.sLo = m5.High, m5.Low
		}
		s.sHi, s.sLo = max(s.sHi, m5.High), min(s.sLo, m5.Low)
		return 0, 0, false
	}
	return s.sHi, s.sLo, s.sHi > 0 && t.Hour() >= to && t.Hour() < 20
}

func (s *rangeFade) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
	hold := strategy.EntryResult{Signal: config.SignalHold}
	h1, m5, bar := states[config.PeriodH1], states[config.PeriodM5], states[s.tf]
	if !h1.IsWarmedUp || !m5.IsWarmedUp || !bar.IsWarmedUp || h1.ATR <= 0 {
		return hold
	}
	if h1.BarTime != s.lastH1 {
		s.lastH1 = h1.BarTime
		s.highs, s.lows = append(s.highs, h1.High), append(s.lows, h1.Low)
		if len(s.highs) > 48 {
			s.highs, s.lows = s.highs[1:], s.lows[1:]
		}
	}
	hi, lo, ok := s.rangeNow(h1, m5)
	if !ok || bar.BarTime == s.lastBar {
		return hold
	}
	s.lastBar = bar.BarTime
	w := hi - lo
	if h1.ADX >= s.maxADX || w < s.minWide*h1.ATR || w > 8*h1.ATR {
		return hold
	}
	if s.coolUntil == nil {
		s.coolUntil = map[string]int64{}
	}
	var dir string
	var sl, tp float64
	switch {
	case bar.High > hi && bar.Close < hi && bar.Close > lo:
		dir, sl = config.SignalSell, bar.High+s.k*h1.ATR
		tp = (hi + lo) / 2
		if s.far {
			tp = lo + 0.1*w
		}
	case bar.Low < lo && bar.Close > lo && bar.Close < hi:
		dir, sl = config.SignalBuy, bar.Low-s.k*h1.ATR
		tp = (hi + lo) / 2
		if s.far {
			tp = hi - 0.1*w
		}
	default:
		return hold
	}
	if m5.BarTime < s.coolUntil[dir] {
		return hold
	}
	buy := dir == config.SignalBuy
	risk, reward := price-sl, tp-price
	if !buy {
		risk, reward = sl-price, price-tp
	}
	if risk <= 0.1 || reward <= 0.1 {
		return hold
	}
	s.coolUntil[dir] = m5.BarTime + 3600
	return strategy.EntryResult{Signal: dir, SLPrice: sl, TPPrice: tp, SLPips: risk / pipSize, TPPips: reward / pipSize,
		ATR: h1.ATR, Confluence: 1, Tier: config.TierNormal}
}
