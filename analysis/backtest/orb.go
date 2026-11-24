package main

import (
	"fmt"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

// Research: US-session opening range breakout. The range is the high/low of the first rangeMin minutes after the anchor
// (New York time); the first M5 close beyond it within entryHours opens a trade, one per day. Stop at the far side
// or middle of the range, target tgt x range. bias: none, ema50 (price vs H1 EMA50), ema200 (price vs H1 EMA200).
type orb struct {
	anchor     int // minutes after NY midnight
	rangeMin   int
	stopMid    bool
	tgt        float64
	bias       string
	watcher    bool
	entryHours int

	day           string
	hi, lo        float64
	ready, traded bool
}

func init() {
	for _, an := range []int{8*60 + 30, 9*60 + 30} {
		for _, rm := range []int{15, 30, 60} {
			for _, mid := range []bool{false, true} {
				for _, tg := range []float64{1, 1.5, 2} {
					for _, bias := range []string{"none", "ema50", "ema200"} {
						for _, w := range []bool{false, true} {
							an, rm, mid, tg, bias, w := an, rm, mid, tg, bias, w
							o := &orb{anchor: an, rangeMin: rm, stopMid: mid, tgt: tg, bias: bias, watcher: w, entryHours: 3}
							strategies[o.Name()] = func() strategy.Strategy {
								return &orb{anchor: an, rangeMin: rm, stopMid: mid, tgt: tg, bias: bias, watcher: w, entryHours: 3}
							}
						}
					}
				}
			}
		}
	}
}

func (s *orb) Name() string {
	stop, w := "far", "nw"
	if s.stopMid {
		stop = "mid"
	}
	if s.watcher {
		w = "w"
	}
	return fmt.Sprintf("orb_%02d%02d_%d_%s_%.1f_%s_%s", s.anchor/60, s.anchor%60, s.rangeMin, stop, s.tgt, s.bias, w)
}
func (s *orb) UsesTrendWatcher() bool { return s.watcher }

func (s *orb) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
	hold := strategy.EntryResult{Signal: config.SignalHold}
	m5, h1 := states[config.PeriodM5], states[config.PeriodH1]
	if !m5.IsWarmedUp || !h1.IsWarmedUp {
		return hold
	}
	open := time.Unix(m5.BarTime, 0).In(newYork)
	if open.Weekday() == time.Saturday || open.Weekday() == time.Sunday {
		return hold
	}
	day := open.Format("2006-01-02")
	if day != s.day {
		s.day, s.hi, s.lo, s.ready, s.traded = day, 0, 0, false, false
	}
	m := open.Hour()*60 + open.Minute()
	end := s.anchor + s.rangeMin
	switch {
	case m < s.anchor:
		return hold
	case m < end:
		if s.hi == 0 {
			s.hi, s.lo = m5.High, m5.Low
		}
		s.hi, s.lo = max(s.hi, m5.High), min(s.lo, m5.Low)
		s.ready = m+5 >= end
		return hold
	}
	if !s.ready || s.traded || m >= end+s.entryHours*60 {
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
	rng := s.hi - s.lo
	if rng <= 0 {
		return hold
	}
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
	tp := price + s.tgt*rng
	if !buy {
		tp = price - s.tgt*rng
	}
	s.traded = true
	return strategy.EntryResult{Signal: dir, SLPrice: sl, TPPrice: tp, SLPips: risk / pipSize, TPPips: s.tgt * rng / pipSize,
		ATR: rng, Confluence: 1, Tier: config.TierNormal}
}
