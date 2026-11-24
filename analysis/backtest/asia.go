package main

import (
	"fmt"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

// Research: Asian range fade. The range is built from 00:00 UTC for buildH hours; until 06:30 UTC, an M5 bar that pokes
// beyond the range and closes back inside is faded (one trade per side per day). Stop buf x range beyond the poke's
// extreme, target the middle ("mid") or the far side ("far"). quiet skips days whose range exceeds quiet x H1 ATR.
type asiaFade struct {
	buildH  int
	buf     float64
	far     bool
	quiet   float64
	watcher bool

	day              string
	hi, lo           float64
	ready            bool
	tradedB, tradedS bool
}

func init() {
	for _, bh := range []int{2, 3, 4} {
		for _, buf := range []float64{0.25, 0.5} {
			for _, far := range []bool{false, true} {
				for _, q := range []float64{0, 2, 1.5} {
					for _, w := range []bool{false, true} {
						bh, buf, far, q, w := bh, buf, far, q, w
						a := &asiaFade{buildH: bh, buf: buf, far: far, quiet: q, watcher: w}
						strategies[a.Name()] = func() strategy.Strategy {
							return &asiaFade{buildH: bh, buf: buf, far: far, quiet: q, watcher: w}
						}
					}
				}
			}
		}
	}
}

func (s *asiaFade) Name() string {
	t, w := "mid", "nw"
	if s.far {
		t = "far"
	}
	if s.watcher {
		w = "w"
	}
	return fmt.Sprintf("asia_%d_%.2f_%s_%.1f_%s", s.buildH, s.buf, t, s.quiet, w)
}
func (s *asiaFade) UsesTrendWatcher() bool { return s.watcher }

func (s *asiaFade) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
	hold := strategy.EntryResult{Signal: config.SignalHold}
	m5, h1 := states[config.PeriodM5], states[config.PeriodH1]
	if !m5.IsWarmedUp || !h1.IsWarmedUp || h1.ATR <= 0 {
		return hold
	}
	open := time.Unix(m5.BarTime, 0).UTC()
	if open.Weekday() == time.Saturday || open.Weekday() == time.Sunday {
		return hold
	}
	if d := open.Format("2006-01-02"); d != s.day {
		s.day, s.hi, s.lo, s.ready, s.tradedB, s.tradedS = d, 0, 0, false, false, false
	}
	m := open.Hour()*60 + open.Minute()
	end := s.buildH * 60
	if m < end {
		if s.hi == 0 {
			s.hi, s.lo = m5.High, m5.Low
		}
		s.hi, s.lo = max(s.hi, m5.High), min(s.lo, m5.Low)
		s.ready = m+5 >= end
		return hold
	}
	rng := s.hi - s.lo
	if !s.ready || m >= 6*60+30 || rng <= 0 || s.quiet > 0 && rng > s.quiet*h1.ATR {
		return hold
	}
	var dir string
	var sl, tp float64
	switch {
	case !s.tradedS && m5.High > s.hi && m5.Close < s.hi:
		dir, sl = config.SignalSell, m5.High+s.buf*rng
		tp = (s.hi + s.lo) / 2
		if s.far {
			tp = s.lo + 0.1*rng
		}
		s.tradedS = true
	case !s.tradedB && m5.Low < s.lo && m5.Close > s.lo:
		dir, sl = config.SignalBuy, m5.Low-s.buf*rng
		tp = (s.hi + s.lo) / 2
		if s.far {
			tp = s.hi - 0.1*rng
		}
		s.tradedB = true
	default:
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
	return strategy.EntryResult{Signal: dir, SLPrice: sl, TPPrice: tp, SLPips: risk / pipSize, TPPips: reward / pipSize,
		ATR: rng, Confluence: 1, Tier: config.TierNormal}
}
