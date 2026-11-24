package main

import (
	"fmt"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

// Research candidates from research/studies/2026-11-24-gold-pattern-search, built with only what the bot knows live.

// monthStart buys the first M5 close after the daily re-open on the first `days` weekdays of the month (a weekday
// calendar fixed in advance, not the data's trading days) and closes before the next daily break.
type monthStart struct {
	days     int
	k        float64
	cap      bool
	lastSess string
}

func (s *monthStart) Name() string {
	return fmt.Sprintf("ms_d%d_k%.1f_cap%v", s.days, s.k, s.cap)
}
func (s *monthStart) UsesTrendWatcher() bool { return false }
func (s *monthStart) MaxHold() time.Duration { return 22*time.Hour + 45*time.Minute }

func weekdayOfMonth(d time.Time) int {
	n := 0
	for x := time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, d.Location()); !x.After(d); x = x.AddDate(0, 0, 1) {
		if x.Weekday() != time.Saturday && x.Weekday() != time.Sunday {
			n++
		}
	}
	return n
}

func (s *monthStart) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
	hold := strategy.EntryResult{Signal: config.SignalHold}
	m5, h1 := states[config.PeriodM5], states[config.PeriodH1]
	if !m5.IsWarmedUp || !h1.IsWarmedUp || h1.ATR <= 0 {
		return hold
	}
	ny := time.Unix(m5.BarTime, 0).In(newYork)
	if ny.Hour() != 18 || ny.Minute() != 0 {
		return hold
	}
	sess := ny.AddDate(0, 0, 1) // the session opening Sunday 18:00 New York is Monday's
	if sess.Weekday() == time.Saturday || sess.Weekday() == time.Sunday || weekdayOfMonth(sess) > s.days {
		return hold
	}
	key := sess.Format(time.DateOnly)
	if key == s.lastSess {
		return hold
	}
	stop := s.k * h1.ATR
	if s.cap && stop > 25 {
		return hold
	}
	s.lastSess = key
	return strategy.EntryResult{Signal: config.SignalBuy, SLPrice: price - stop, TPPrice: price + 10*h1.ATR,
		SLPips: stop / pipSize, TPPips: 10 * h1.ATR / pipSize, ATR: h1.ATR, Confluence: 1, Tier: config.TierNormal}
}

// eveningDip buys after a strong down H1 candle whose New York open hour is in [from, to], holds `hold`, and only
// enters when the hold ends before the 17:00 New York break.
type eveningDip struct {
	from, to int
	hold     time.Duration
	k        float64
	cap      bool
	lastH1   int64
}

func (s *eveningDip) Name() string {
	return fmt.Sprintf("ed_%02d-%02d_h%.0f_k%.1f_cap%v", s.from, s.to, s.hold.Hours(), s.k, s.cap)
}
func (s *eveningDip) UsesTrendWatcher() bool { return false }
func (s *eveningDip) MaxHold() time.Duration { return s.hold }

func (s *eveningDip) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
	hold := strategy.EntryResult{Signal: config.SignalHold}
	m5, h1 := states[config.PeriodM5], states[config.PeriodH1]
	if !m5.IsWarmedUp || !h1.IsWarmedUp || h1.ATR <= 0 || h1.BarTime == s.lastH1 {
		return hold
	}
	if m5.BarTime+300 != h1.BarTime+3600 { // act only on the M5 close that is also the H1 close
		return hold
	}
	s.lastH1 = h1.BarTime
	open := time.Unix(h1.BarTime, 0).In(newYork)
	if open.Weekday() == time.Saturday || open.Weekday() == time.Sunday || open.Hour() < s.from || open.Hour() > s.to {
		return hold
	}
	rg := h1.High - h1.Low
	if rg <= 0 || h1.Close >= h1.Open || h1.Open-h1.Close <= 0.6*rg {
		return hold
	}
	now := time.Unix(h1.BarTime+3600, 0).In(newYork)
	brk := time.Date(now.Year(), now.Month(), now.Day(), 16, 55, 0, 0, newYork)
	if now.Add(s.hold).After(brk) {
		return hold
	}
	stop := s.k * h1.ATR
	if s.cap && stop > 25 {
		return hold
	}
	return strategy.EntryResult{Signal: config.SignalBuy, SLPrice: price - stop, TPPrice: price + 10*h1.ATR,
		SLPips: stop / pipSize, TPPips: 10 * h1.ATR / pipSize, ATR: h1.ATR, Confluence: 1, Tier: config.TierNormal}
}

func init() {
	for _, d := range []int{1, 2, 3, 5} {
		for _, k := range []float64{1, 2, 3} {
			for _, c := range []bool{false, true} {
				d, k, c := d, k, c
				strategies[(&monthStart{days: d, k: k, cap: c}).Name()] = func() strategy.Strategy { return &monthStart{days: d, k: k, cap: c} }
			}
		}
	}
	for _, hr := range [][2]int{{10, 12}, {11, 12}, {12, 13}, {11, 13}, {10, 13}} {
		for _, h := range []int{2, 3, 4} {
			for _, k := range []float64{1, 1.5, 2} {
				for _, c := range []bool{false, true} {
					hr, h, k, c := hr, h, k, c
					mk := func() strategy.Strategy {
						return &eveningDip{from: hr[0], to: hr[1], hold: time.Duration(h) * time.Hour, k: k, cap: c}
					}
					strategies[mk().Name()] = mk
				}
			}
		}
	}
}
