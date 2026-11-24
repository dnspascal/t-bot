package reopenhour

import (
	"math"
	"time"
	_ "time/tzdata" // the server may not ship tzdata; New York time decides the re-open

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

// Buys gold after the broker's daily re-open (the New York 17:00-18:00 break) and closes it after MaxHold.
// research/studies/2026-11-24-gold-holding: the first hour after the re-open averaged +2.9 bp, 2016-2025.
const (
	MaxHold         = 60 * time.Minute
	slATRMult       = 1.0
	tpATRMult       = 3.0  // far away on purpose: the exit is the 60-minute time stop
	MaxStopDistance = 25.0 // price units ($ per 1 oz)
)

var newYork = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(err)
	}
	return loc
}()

type ReopenHour struct {
	slATR      float64
	weeklyOpen bool // also trade the Sunday evening weekly open
	lastDay    string
}

func New() *ReopenHour { return &ReopenHour{slATR: slATRMult, weeklyOpen: true} }

// NewVariant is for research grids.
func NewVariant(slATR float64, weeklyOpen bool) *ReopenHour {
	return &ReopenHour{slATR: slATR, weeklyOpen: weeklyOpen}
}

func (s *ReopenHour) Name() string           { return "reopen_hour" }
func (s *ReopenHour) UsesTrendWatcher() bool { return false }
func (s *ReopenHour) MaxHold() time.Duration { return MaxHold }

func (s *ReopenHour) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
	hold := func(why string) strategy.EntryResult {
		return strategy.EntryResult{Signal: config.SignalHold, Reason: why}
	}
	m5, h1 := states[config.PeriodM5], states[config.PeriodH1]
	if !m5.IsWarmedUp || !h1.IsWarmedUp || h1.ATR <= 0 {
		return hold("indicators not warmed up")
	}
	ny := time.Unix(m5.BarTime, 0).In(newYork)
	if ny.Hour() != 18 || ny.Minute() != 0 {
		return hold("not the first M5 bar after the daily re-open")
	}
	switch wd := ny.Weekday(); {
	case wd == time.Friday || wd == time.Saturday:
		return hold("no session re-open on Friday/Saturday evening")
	case wd == time.Sunday && !s.weeklyOpen:
		return hold("weekly open excluded")
	}
	day := ny.Format(time.DateOnly)
	if day == s.lastDay {
		return hold("already traded this re-open")
	}
	stop := s.slATR * h1.ATR
	if stop > MaxStopDistance {
		return hold("stop would be wider than the per-trade risk cap")
	}
	s.lastDay = day
	tp := tpATRMult * h1.ATR
	return strategy.EntryResult{
		Signal: config.SignalBuy, SLPrice: price - stop, TPPrice: price + tp,
		SLPips: stop / pipSize, TPPips: tp / pipSize, ATR: h1.ATR, Confluence: 1, Tier: config.TierNormal,
		Rules: map[string]strategy.RuleCheck{
			"ny_reopen_bar": {Value: float64(ny.Hour()*100 + ny.Minute()), Limit: 1800,
				Why: "first M5 bar after the New York 17:00-18:00 break; buy, close after 60 minutes"},
			"stop_distance": {Value: math.Round(stop*100) / 100, Limit: MaxStopDistance,
				Why: "stop distance in $ per 1 oz (1 x H1 ATR); must be <= limit (risk cap)"},
		},
	}
}
