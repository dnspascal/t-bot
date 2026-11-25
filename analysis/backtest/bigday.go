package main

import (
	"fmt"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
	trendfollow "github.com/denismgaya/t-bot/internal/strategy/trend_follow"
)

// Research: trend_follow that only enters when today's range so far is >= k x the average range of the previous 20
// days. Today = UTC day or the broker's New York day (17:00 New York to 17:00). The inner strategy is evaluated on
// every bar, so its own state (streaks, cooldowns) is exactly as live.
type bigDay struct {
	inner   *trendfollow.TrendFollow
	k       float64
	nyDay   bool
	day     string
	hi, lo  float64
	past    []float64
	lastBar int64
}

func (s *bigDay) Name() string {
	d := "utc"
	if s.nyDay {
		d = "ny"
	}
	return fmt.Sprintf("tfbig_%s_k%.2f", d, s.k)
}
func (s *bigDay) UsesTrendWatcher() bool { return true }
func (s *bigDay) OnClosed(side, reason string, at time.Time) {
	s.inner.OnClosed(side, reason, at)
}

func (s *bigDay) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
	m5 := states[config.PeriodM5]
	if m5.IsWarmedUp && m5.BarTime != s.lastBar {
		s.lastBar = m5.BarTime
		t := time.Unix(m5.BarTime, 0).UTC()
		if s.nyDay {
			t = time.Unix(m5.BarTime, 0).In(newYork).Add(7 * time.Hour) // 17:00 New York starts the next day
		}
		d := t.Format(time.DateOnly)
		if d != s.day {
			if s.day != "" && s.hi > s.lo {
				s.past = append(s.past, s.hi-s.lo)
				if len(s.past) > 20 {
					s.past = s.past[1:]
				}
			}
			s.day, s.hi, s.lo = d, m5.High, m5.Low
		}
		s.hi, s.lo = max(s.hi, m5.High), min(s.lo, m5.Low)
	}
	r := s.inner.Evaluate(states, price, pipSize)
	if r.Signal != config.SignalBuy && r.Signal != config.SignalSell {
		return r
	}
	if len(s.past) < 20 {
		return strategy.EntryResult{Signal: config.SignalHold}
	}
	adr := 0.0
	for _, v := range s.past {
		adr += v
	}
	adr /= float64(len(s.past))
	if s.hi-s.lo < s.k*adr {
		return strategy.EntryResult{Signal: config.SignalHold}
	}
	return r
}

func init() {
	for _, ny := range []bool{false, true} {
		for _, k := range []float64{1.0, 1.25, 1.5, 1.75, 2.0} {
			ny, k := ny, k
			mk := func() strategy.Strategy { return &bigDay{inner: trendfollow.New(), k: k, nyDay: ny} }
			strategies[mk().Name()] = mk
		}
	}
}
