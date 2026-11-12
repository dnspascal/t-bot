package sweep

import (
	"math"
	"testing"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

var (
	h1Up   = indicator.MarketState{IsWarmedUp: true, Regime: config.TrendingUp, ADX: 18}
	h1Down = indicator.MarketState{IsWarmedUp: true, Regime: config.TrendingDown, ADX: 18}
)

func bar(t int64, high, low, close, adx float64) indicator.MarketState {
	return indicator.MarketState{IsWarmedUp: true, BarTime: t, ATR: 5, ADX: adx,
		RangeHigh: 110, RangeLow: 100, High: high, Low: low, Close: close}
}

func eval(s *Sweep, m15, h1 indicator.MarketState, price float64) strategy.EntryResult {
	return s.Evaluate(map[string]indicator.MarketState{config.PeriodM15: m15, config.PeriodH1: h1}, price, 0.01)
}

// Swept low: dipped to 98 under the 100 range low, closed at 103 near the bar's high of 104.
var sweptLow = bar(900, 104, 98, 103, 25)

func TestBuySweepConfirmed(t *testing.T) {
	s := New()
	if r := eval(s, sweptLow, h1Up, 103); r.Reason != "range low swept — awaiting next-bar confirmation" {
		t.Fatalf("sweep bar: got %q", r.Reason)
	}
	r := eval(s, bar(1800, 105, 101, 104, 25), h1Up, 104)
	if r.Signal != config.SignalBuy {
		t.Fatalf("confirmation: got %s (%s), want BUY", r.Signal, r.Reason)
	}
	if r.SLPrice != 99 || r.TPPrice != 114 {
		t.Errorf("SL/TP = %v/%v, want 99/114 (1 and 2 M15 ATR)", r.SLPrice, r.TPPrice)
	}
	want := map[string]float64{"range_level": 100, "sweep_depth_atr": 0.4, "sweep_reject_wick": 1.0 / 6, "m15_adx": 25, "confirm_inside_atr": 0.8, "h1_adx": 18}
	for k, v := range want {
		got, ok := r.Rules[k]
		if !ok || got.Why == "" || math.Abs(got.Value-v) > 1e-3 {
			t.Errorf("rule %s = %+v, want value %v with a description", k, got, v)
		}
	}
}

func TestSellSweepConfirmed(t *testing.T) {
	s := New()
	eval(s, bar(900, 112, 106, 107, 25), h1Down, 107) // swept 110 high, closed near the low
	if r := eval(s, bar(1800, 109, 105, 106, 25), h1Down, 106); r.Signal != config.SignalSell {
		t.Fatalf("got %s (%s), want SELL", r.Signal, r.Reason)
	}
}

func TestNoTrade(t *testing.T) {
	cases := []struct {
		name      string
		sweepBar  indicator.MarketState
		confirm   indicator.MarketState
		h1        indicator.MarketState
		wantSweep string
		want      string
	}{
		{"next bar breaks out for real", sweptLow, bar(1800, 101, 96, 97, 25), h1Up, "",
			"sweep not confirmed — next bar closed beyond the level (real breakout)"},
		{"H1 trend against the trade", sweptLow, bar(1800, 105, 101, 104, 25), h1Down, "",
			"swept low but H1 not trending up"},
		{"confirmation ADX too low", sweptLow, bar(1800, 105, 101, 104, 15), h1Up, "",
			"M15 ADX outside 20-35 on the confirmation bar"},
		{"weak rejection close", bar(900, 104, 98, 100.5, 25), indicator.MarketState{}, h1Up,
			"range low swept but bar closed weakly — no clear rejection", ""},
		{"true breakout is not a sweep", bar(900, 104, 96, 97, 25), indicator.MarketState{}, h1Up, "no sweep", ""},
		{"already trending on M15", bar(900, 104, 98, 103, 40), indicator.MarketState{}, h1Up,
			"M15 ADX too high — already trending, not a range", ""},
	}
	for _, c := range cases {
		s := New()
		r := eval(s, c.sweepBar, c.h1, c.sweepBar.Close)
		if c.wantSweep != "" {
			if r.Reason != c.wantSweep {
				t.Errorf("%s: sweep bar got %q, want %q", c.name, r.Reason, c.wantSweep)
			}
			continue
		}
		if r := eval(s, c.confirm, c.h1, c.confirm.Close); r.Signal != config.SignalHold || r.Reason != c.want {
			t.Errorf("%s: got %s %q, want HOLD %q", c.name, r.Signal, r.Reason, c.want)
		}
	}
}

func TestSameBarIsNotReused(t *testing.T) {
	s := New()
	eval(s, sweptLow, h1Up, 103)
	if r := eval(s, sweptLow, h1Up, 103); r.Signal != config.SignalHold || r.Reason != "waiting for the next M15 bar" {
		t.Errorf("re-evaluating the sweep bar: got %s %q", r.Signal, r.Reason)
	}
}

func TestCooldownAfterTwoFailures(t *testing.T) {
	s := New()
	closeAt := time.Unix(1000, 0)
	s.OnClosed(config.SignalBuy, strategy.CloseReasonSLHit, closeAt)
	s.OnClosed(config.SignalBuy, strategy.CloseReasonSLHit, closeAt)
	eval(s, sweptLow, h1Up, 103)
	if r := eval(s, bar(1800, 105, 101, 104, 25), h1Up, 104); r.Reason != "cooling down after repeated same-direction invalidation" {
		t.Errorf("got %q, want cooldown", r.Reason)
	}
}
