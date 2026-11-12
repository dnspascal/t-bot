package breakout

import (
	"testing"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
)

func TestRejectWick(t *testing.T) {
	b := indicator.MarketState{High: 110, Low: 100, Close: 104}
	if w := rejectWick(b, config.SignalBuy); w != 0.6 {
		t.Errorf("buy wick = %v, want 0.6", w)
	}
	if w := rejectWick(b, config.SignalSell); w != 0.4 {
		t.Errorf("sell wick = %v, want 0.4", w)
	}
}

func eval(s *Breakout, m15, h1 indicator.MarketState) string {
	r := s.Evaluate(map[string]indicator.MarketState{config.PeriodM15: m15, config.PeriodH1: h1}, m15.Close, 0.01)
	return r.Reason
}

func TestFakeoutFilters(t *testing.T) {
	h1 := indicator.MarketState{IsWarmedUp: true, Regime: config.TrendingUp}
	brk := indicator.MarketState{IsWarmedUp: true, ATR: 5, ADX: 25, BarTime: 900, RangeHigh: 100, RangeLow: 90, High: 104, Low: 99, Close: 103.5}

	failed := brk
	failed.Close = 99.5 // poked above 100 but closed back inside: sweep territory
	if r := eval(New(), failed, h1); r != "no close beyond the M15 range — failed breaks are traded by sweep" {
		t.Errorf("failed break: got %q", r)
	}

	rejected := brk
	rejected.Close = 100.5 // wick (104-100.5)/5 = 0.7
	if r := eval(New(), rejected, h1); r != "breakout bar rejected — closed well back from its extreme" {
		t.Errorf("rejected bar: got %q", r)
	}

	conf := indicator.MarketState{IsWarmedUp: true, ATR: 5, ADX: 25, BarTime: 1800, RangeHigh: 104, RangeLow: 90, High: 105, Low: 103, Close: 104}
	s := New()
	eval(s, brk, h1)
	if r := s.Evaluate(map[string]indicator.MarketState{config.PeriodM15: conf, config.PeriodH1: h1}, 104, 0.01); r.Signal != config.SignalBuy {
		t.Fatalf("clean breakout: got %s (%s), want BUY", r.Signal, r.Reason)
	} else if r.Rules["m15_breakout_reject_wick"].Value != 0.1 {
		t.Errorf("recorded wick = %v, want 0.1", r.Rules["m15_breakout_reject_wick"].Value)
	}

	weak := conf
	weak.ADX = 15
	s = New()
	eval(s, brk, h1)
	if r := eval(s, weak, h1); r != "M15 ADX too low — breakout has no momentum behind it" {
		t.Errorf("weak ADX: got %q", r)
	}
}
