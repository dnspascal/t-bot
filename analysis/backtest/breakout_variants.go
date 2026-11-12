package main

import (
	"fmt"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

// Breakout entry variants for research only. Same range break, filters and SL/TP
// as internal/strategy/breakout; they differ in range length and entry timing.
type boVariant struct {
	lookback int
	mode     string // confirm | retest | nochase

	bars     []indicator.OHLC
	lastBar  int64
	pend     *boSetup // broke the level, waiting for the confirmation close
	wait     *boSetup // confirmed, waiting for the retest (retest mode)
	failDir  string
	fails    int
	coolTill time.Time
}

type boSetup struct {
	dir     string
	level   float64
	expires int64
}

const (
	boWickMax     = 0.4
	boADXMin      = 20.0
	boADXMax      = 35.0
	boRetestBand  = 0.1  // ATR: how close price must come back to the level
	boRetestFail  = 0.25 // ATR: closing this far back inside the range cancels the setup
	boRetestWait  = 30 * 60
	boNoChaseDist = 0.5 // ATR
)

func init() {
	for _, lb := range []int{6, 8, 10, 15, 20} {
		for _, mode := range []string{"confirm", "retest", "nochase", "fade", "fadenoadx"} {
			lb, mode := lb, mode
			strategies[fmt.Sprintf("bo_%s_%d", mode, lb)] = func() strategy.Strategy { return &boVariant{lookback: lb, mode: mode} }
		}
	}
}

func (s *boVariant) Name() string           { return fmt.Sprintf("bo_%s_%d", s.mode, s.lookback) }
func (s *boVariant) UsesTrendWatcher() bool { return true }

func (s *boVariant) OnClosed(side, reason string, at time.Time) {
	switch strategy.ClassifyCloseReason(reason) {
	case strategy.CloseInvalidated:
		if s.failDir != side {
			s.failDir, s.fails = side, 0
		}
		if s.fails++; s.fails >= 2 {
			s.coolTill = at.Add(30 * time.Minute)
		}
	case strategy.CloseValidated:
		if s.failDir == side {
			s.fails, s.coolTill = 0, time.Time{}
		}
	}
}

func (s *boVariant) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
	hold := strategy.EntryResult{Signal: config.SignalHold}
	m15, h1, m5 := states[config.PeriodM15], states[config.PeriodH1], states[config.PeriodM5]
	if !m15.IsWarmedUp || m15.ATR <= 0 || !h1.IsWarmedUp {
		return hold
	}
	atr := m15.ATR

	if s.mode == "retest" && s.wait != nil {
		w := s.wait
		sg := 1.0
		if w.dir == config.SignalSell {
			sg = -1
		}
		switch {
		case m5.BarTime >= w.expires || sg*(m5.Close-w.level) < -boRetestFail*atr:
			s.wait = nil
		case sg*(m5.Low-w.level) <= boRetestBand*atr && sg*(m5.High-w.level) >= -boRetestBand*atr && sg*(m5.Close-w.level) > 0:
			s.wait = nil
			return s.enter(w.dir, price, atr, m15.BarTime)
		}
	}

	if m15.BarTime == s.lastBar {
		return hold
	}
	s.lastBar = m15.BarTime
	bar := indicator.OHLC{Open: m15.Open, High: m15.High, Low: m15.Low, Close: m15.Close}
	defer func() {
		s.bars = append(s.bars, bar)
		if len(s.bars) > s.lookback {
			s.bars = s.bars[1:]
		}
	}()

	if p := s.pend; p != nil {
		s.pend = nil
		held := p.dir == config.SignalBuy && m15.Close > p.level || p.dir == config.SignalSell && m15.Close < p.level
		trendOK := p.dir == config.SignalBuy && h1.Regime == config.TrendingUp || p.dir == config.SignalSell && h1.Regime == config.TrendingDown
		adxOK := m15.ADX >= boADXMin && m15.ADX <= boADXMax
		if s.mode == "fadenoadx" {
			adxOK = true
		}
		if held && trendOK && adxOK {
			switch s.mode {
			case "confirm", "fade", "fadenoadx":
				return s.enter(p.dir, price, atr, m15.BarTime)
			case "nochase":
				if d := price - p.level; d <= boNoChaseDist*atr && d >= -boNoChaseDist*atr {
					return s.enter(p.dir, price, atr, m15.BarTime)
				}
			case "retest":
				s.wait = &boSetup{dir: p.dir, level: p.level, expires: m15.BarTime + 900 + boRetestWait}
			}
		}
		return hold
	}

	if len(s.bars) < s.lookback || m15.ADX > boADXMax {
		return hold
	}
	hi, lo := s.bars[0].High, s.bars[0].Low
	for _, b := range s.bars[1:] {
		hi, lo = max(hi, b.High), min(lo, b.Low)
	}
	rng := m15.High - m15.Low
	if rng <= 0 {
		return hold
	}
	if s.mode == "fade" || s.mode == "fadenoadx" {
		switch {
		case m15.High > hi && m15.Close < hi && (m15.Close-m15.Low)/rng < boWickMax:
			s.pend = &boSetup{dir: config.SignalSell, level: hi} // swept the range high, closed back inside
		case m15.Low < lo && m15.Close > lo && (m15.High-m15.Close)/rng < boWickMax:
			s.pend = &boSetup{dir: config.SignalBuy, level: lo} // swept the range low, closed back inside
		}
		return hold
	}
	switch {
	case m15.Close > hi && (m15.High-m15.Close)/rng < boWickMax:
		s.pend = &boSetup{dir: config.SignalBuy, level: hi}
	case m15.Close < lo && (m15.Close-m15.Low)/rng < boWickMax:
		s.pend = &boSetup{dir: config.SignalSell, level: lo}
	}
	return hold
}

func (s *boVariant) enter(dir string, price, atr float64, barTime int64) strategy.EntryResult {
	if dir == s.failDir && time.Unix(barTime, 0).Before(s.coolTill) {
		return strategy.EntryResult{Signal: config.SignalHold}
	}
	r := strategy.EntryResult{Signal: dir, ATR: atr, Tier: config.TierNormal}
	if dir == config.SignalBuy {
		r.SLPrice, r.TPPrice = price-atr, price+2*atr
	} else {
		r.SLPrice, r.TPPrice = price+atr, price-2*atr
	}
	return r
}
