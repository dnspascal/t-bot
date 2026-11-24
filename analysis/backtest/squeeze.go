package main

import (
	"fmt"
	"sort"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

// Research: breakout after compression. At each H1 close the box is the high/low of the last boxH H1 bars; it is a
// squeeze when its height ranks in the lowest pct of the last 120 such boxes. The first M5 close beyond a squeeze box
// opens one trade (re-armed only after the squeeze ends). Stop far side or middle, target tgt x box height.
type squeeze struct {
	boxH    int
	pct     float64
	stopMid bool
	tgt     float64
	bias    string

	lastH1         int64
	highs, lows    []float64
	heights        []float64
	active, traded bool
	hi, lo         float64
}

func init() {
	for _, bh := range []int{4, 6, 8} {
		for _, p := range []float64{0.1, 0.2, 0.3} {
			for _, mid := range []bool{false, true} {
				for _, tg := range []float64{1.5, 2, 3} {
					for _, bias := range []string{"none", "ema50", "ema200"} {
						bh, p, mid, tg, bias := bh, p, mid, tg, bias
						q := &squeeze{boxH: bh, pct: p, stopMid: mid, tgt: tg, bias: bias}
						strategies[q.Name()] = func() strategy.Strategy {
							return &squeeze{boxH: bh, pct: p, stopMid: mid, tgt: tg, bias: bias}
						}
					}
				}
			}
		}
	}
}

func (s *squeeze) Name() string {
	stop := "far"
	if s.stopMid {
		stop = "mid"
	}
	return fmt.Sprintf("sqz_%d_%.1f_%s_%.1f_%s", s.boxH, s.pct, stop, s.tgt, s.bias)
}
func (s *squeeze) UsesTrendWatcher() bool { return false }

func (s *squeeze) onH1(h1 indicator.MarketState) {
	s.highs, s.lows = append(s.highs, h1.High), append(s.lows, h1.Low)
	if len(s.highs) > s.boxH {
		s.highs, s.lows = s.highs[1:], s.lows[1:]
	}
	if len(s.highs) < s.boxH {
		return
	}
	hi, lo := s.highs[0], s.lows[0]
	for i := range s.highs {
		hi, lo = max(hi, s.highs[i]), min(lo, s.lows[i])
	}
	h := hi - lo
	rank := 1.0
	if len(s.heights) >= 60 {
		sorted := append([]float64(nil), s.heights...)
		sort.Float64s(sorted)
		rank = float64(sort.SearchFloat64s(sorted, h)) / float64(len(sorted))
	}
	s.heights = append(s.heights, h)
	if len(s.heights) > 120 {
		s.heights = s.heights[1:]
	}
	was := s.active
	s.active = rank <= s.pct
	if s.active {
		s.hi, s.lo = hi, lo
	}
	if !s.active && was {
		s.traded = false
	}
}

func (s *squeeze) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
	hold := strategy.EntryResult{Signal: config.SignalHold}
	m5, h1 := states[config.PeriodM5], states[config.PeriodH1]
	if !m5.IsWarmedUp || !h1.IsWarmedUp {
		return hold
	}
	if h1.BarTime != s.lastH1 {
		s.lastH1 = h1.BarTime
		s.onH1(h1)
	}
	if !s.active || s.traded || s.hi <= s.lo {
		return hold
	}
	dir := ""
	switch {
	case m5.Close > s.hi:
		dir = config.SignalBuy
	case m5.Close < s.lo:
		dir = config.SignalSell
	default:
		return hold
	}
	buy := dir == config.SignalBuy
	switch s.bias {
	case "ema50":
		if h1.EMA50 == 0 || buy && price < h1.EMA50 || !buy && price > h1.EMA50 {
			return hold
		}
	case "ema200":
		if h1.EMA200 == 0 || buy && price < h1.EMA200 || !buy && price > h1.EMA200 {
			return hold
		}
	}
	box := s.hi - s.lo
	sl := s.lo
	if !buy {
		sl = s.hi
	}
	if s.stopMid {
		sl = (s.hi + s.lo) / 2
	}
	risk := price - sl
	if !buy {
		risk = sl - price
	}
	if risk <= 0.1 {
		return hold
	}
	tp := price + s.tgt*box
	if !buy {
		tp = price - s.tgt*box
	}
	s.traded = true
	return strategy.EntryResult{Signal: dir, SLPrice: sl, TPPrice: tp, SLPips: risk / pipSize, TPPips: s.tgt * box / pipSize,
		ATR: box, Confluence: 1, Tier: config.TierNormal}
}
