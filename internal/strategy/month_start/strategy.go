package monthstart

import (
	"math"
	"time"
	_ "time/tzdata" // the server may not ship tzdata; New York time decides the session

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

// Buys gold at the start of each of the first 3 weekdays of the month and closes before the next daily break.
// Backtested as ms_d3_k1.0_captrue (research/studies/2026-11-24-gold-candidates).
const (
	Days            = 3
	MaxHold         = 22*time.Hour + 45*time.Minute // re-open 18:05 New York to 16:50, before the next break
	slATRMult       = 1.0
	tpATRMult       = 10.0 // far away on purpose: the exit is the end of the session
	MaxStopDistance = 25.0 // price units ($ per 1 oz)
)

var newYork = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(err)
	}
	return loc
}()

type MonthStart struct {
	lastSession string
}

func New() *MonthStart { return &MonthStart{} }

func (s *MonthStart) Name() string           { return "month_start" }
func (s *MonthStart) UsesTrendWatcher() bool { return false }
func (s *MonthStart) MaxHold() time.Duration { return MaxHold }

// WeekdayOfMonth counts Monday-Friday dates from the 1st up to d (a fixed calendar, holidays included).
func WeekdayOfMonth(d time.Time) int {
	n := 0
	for x := time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, d.Location()); !x.After(d); x = x.AddDate(0, 0, 1) {
		if x.Weekday() != time.Saturday && x.Weekday() != time.Sunday {
			n++
		}
	}
	return n
}

func (s *MonthStart) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
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
	session := ny.AddDate(0, 0, 1) // the session opening Sunday 18:00 New York is Monday's
	if session.Weekday() == time.Saturday || session.Weekday() == time.Sunday {
		return hold("no weekend session")
	}
	wd := WeekdayOfMonth(session)
	if wd > Days {
		return hold("not one of the first 3 weekdays of the month")
	}
	key := session.Format(time.DateOnly)
	if key == s.lastSession {
		return hold("already traded this session")
	}
	stop := slATRMult * h1.ATR
	if stop > MaxStopDistance {
		return hold("stop would be wider than the per-trade risk cap")
	}
	s.lastSession = key
	tp := tpATRMult * h1.ATR
	return strategy.EntryResult{
		Signal: config.SignalBuy, SLPrice: price - stop, TPPrice: price + tp,
		SLPips: stop / pipSize, TPPips: tp / pipSize, ATR: h1.ATR, Confluence: 1, Tier: config.TierNormal,
		Rules: map[string]strategy.RuleCheck{
			"weekday_of_month": {Value: float64(wd), Limit: Days,
				Why: "Monday-Friday dates since the 1st, counting this session; must be <= limit"},
			"stop_distance": {Value: math.Round(stop*100) / 100, Limit: MaxStopDistance,
				Why: "stop distance in $ per 1 oz (1 x H1 ATR); must be <= limit (risk cap)"},
		},
	}
}
