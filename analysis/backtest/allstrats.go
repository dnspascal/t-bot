package main

import (
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
	ddearlybreakout "github.com/denismgaya/t-bot/internal/strategy/dd_early_breakout"
	ddrangingbreakout "github.com/denismgaya/t-bot/internal/strategy/dd_ranging_breakout"
	rsireversal "github.com/denismgaya/t-bot/internal/strategy/rsi_reversal"
	srbounce "github.com/denismgaya/t-bot/internal/strategy/sr_bounce"
)

// The rest of the live strategy set. sr_bounce runs without its ONNX filter here (gold symbol id 1).
func init() {
	strategies["sr_bounce"] = func() strategy.Strategy { return srbounce.New(nil, 0, 1) }
	strategies["rsi_reversal"] = func() strategy.Strategy { return rsireversal.New() }
	strategies["dd_early_breakout"] = func() strategy.Strategy { return ddearlybreakout.New() }
	strategies["dd_ranging_breakout"] = func() strategy.Strategy { return ddrangingbreakout.New() }
}

// sessionRange mirrors marketstate.Processor: the high/low of the hour before the London and NY opens, exposed as
// SessionHigh/Low on bars that start inside the first 30 minutes of the session.
type sessionRange struct{ lonHi, lonLo, nyHi, nyLo float64 }

func (s *sessionRange) apply(barStart int64, high, low float64, ms *indicator.MarketState) {
	t := time.Unix(barStart, 0).UTC()
	switch config.PreSessionHour(t) {
	case "london":
		if s.lonHi == 0 || high > s.lonHi {
			s.lonHi = high
		}
		if s.lonLo == 0 || low < s.lonLo {
			s.lonLo = low
		}
	case "ny":
		if s.nyHi == 0 || high > s.nyHi {
			s.nyHi = high
		}
		if s.nyLo == 0 || low < s.nyLo {
			s.nyLo = low
		}
	}
	if l := t.In(config.LondonLoc); l.Hour() == config.LondonOpenHour && l.Minute() >= config.SessionOpenMins {
		s.lonHi, s.lonLo = 0, 0
	}
	if n := t.In(config.NewYorkLoc); n.Hour() == config.NewYorkOpenHour && n.Minute() >= config.SessionOpenMins {
		s.nyHi, s.nyLo = 0, 0
	}
	switch config.SessionOpen(t) {
	case "london":
		if s.lonHi > 0 {
			ms.SessionHigh, ms.SessionLow = s.lonHi, s.lonLo
		}
	case "ny":
		if s.nyHi > 0 {
			ms.SessionHigh, ms.SessionLow = s.nyHi, s.nyLo
		}
	}
}
