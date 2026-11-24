package main

import (
	"fmt"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

// Research: previous-day high/low. brk: the first M5 close beyond yesterday's high (low) buys (sells), stop k x M15 ATR
// back inside the level. rej: an M5 bar that pokes beyond the level and closes back inside fades it, stop k x M15 ATR
// beyond the poke. Target tgt x risk, one trade per level per day, optional H1 EMA bias.
type pdl struct {
	mode string
	k    float64
	tgt  float64
	bias string

	day          float64
	doneH, doneL bool
}

func init() {
	for _, mode := range []string{"brk", "rej"} {
		for _, k := range []float64{0.5, 1} {
			for _, tg := range []float64{1.5, 2, 3} {
				for _, bias := range []string{"none", "ema50", "ema200"} {
					mode, k, tg, bias := mode, k, tg, bias
					p := &pdl{mode: mode, k: k, tgt: tg, bias: bias}
					strategies[p.Name()] = func() strategy.Strategy { return &pdl{mode: mode, k: k, tgt: tg, bias: bias} }
				}
			}
		}
	}
}

func (s *pdl) Name() string {
	return fmt.Sprintf("pdl_%s_%.1f_%.1f_%s", s.mode, s.k, s.tgt, s.bias)
}
func (s *pdl) UsesTrendWatcher() bool { return s.mode == "brk" }

func (s *pdl) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
	hold := strategy.EntryResult{Signal: config.SignalHold}
	m5, m15, h1 := states[config.PeriodM5], states[config.PeriodM15], states[config.PeriodH1]
	if !m5.IsWarmedUp || !m15.IsWarmedUp || m15.ATR <= 0 || h1.PrevDayHigh == 0 {
		return hold
	}
	if key := h1.PrevDayHigh*1e6 + h1.PrevDayLow; key != s.day {
		s.day, s.doneH, s.doneL = key, false, false
	}
	hiL, loL, a := h1.PrevDayHigh, h1.PrevDayLow, m15.ATR
	var dir string
	var sl float64
	switch s.mode {
	case "brk":
		switch {
		case !s.doneH && m5.Close > hiL && m5.Open <= hiL:
			dir, sl, s.doneH = config.SignalBuy, hiL-s.k*a, true
		case !s.doneL && m5.Close < loL && m5.Open >= loL:
			dir, sl, s.doneL = config.SignalSell, loL+s.k*a, true
		}
	case "rej":
		switch {
		case !s.doneH && m5.High > hiL && m5.Close < hiL:
			dir, sl, s.doneH = config.SignalSell, m5.High+s.k*a, true
		case !s.doneL && m5.Low < loL && m5.Close > loL:
			dir, sl, s.doneL = config.SignalBuy, m5.Low-s.k*a, true
		}
	}
	if dir == "" {
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
	risk := price - sl
	if !buy {
		risk = sl - price
	}
	if risk <= 0.1 {
		return hold
	}
	tp := price + s.tgt*risk
	if !buy {
		tp = price - s.tgt*risk
	}
	return strategy.EntryResult{Signal: dir, SLPrice: sl, TPPrice: tp, SLPips: risk / pipSize, TPPips: s.tgt * risk / pipSize,
		ATR: a, Confluence: 1, Tier: config.TierNormal}
}
