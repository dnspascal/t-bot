package trendfollow

import (
	"math"
	"testing"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

func baseH1() indicator.MarketState {
	return indicator.MarketState{
		IsWarmedUp: true, Regime: config.TrendingUp, ADX: 30, ATR: 20,
		EMAFast: 4010, EMASlow: 4000, EMA50: 3990,
		RecentHigh: 4005, RecentLow: 3950, RecentEMAGapMax: 12, RecentEMAGapMin: 4,
	}
}

// Feeds 3 M5 bars (ranging, ranging, trending_up) so the freshness gate passes, returns the last result.
func evalBuy(h1 indicator.MarketState, price float64) strategyResult {
	s := New()
	m5 := indicator.MarketState{IsWarmedUp: true, EMAFast: 101, EMASlow: 100}
	m15 := indicator.MarketState{IsWarmedUp: true, ATR: 5, BarTime: 1000}
	var r strategyResult
	for i, reg := range []string{config.Ranging, config.Ranging, config.TrendingUp} {
		m5.BarTime, m5.Regime = int64(i+1), reg
		states := map[string]indicator.MarketState{config.PeriodH1: h1, config.PeriodM5: m5, config.PeriodM15: m15}
		res := s.Evaluate(states, price, 0.01)
		r = strategyResult{res.Signal, res.Reason, res.Rules}
	}
	return r
}

type strategyResult struct {
	signal, reason string
	rules          map[string]strategy.RuleCheck
}

func TestBaseSetupBuys(t *testing.T) {
	if r := evalBuy(baseH1(), 4000); r.signal != config.SignalBuy {
		t.Fatalf("base setup: got %s (%s), want BUY", r.signal, r.reason)
	}
}

func TestH1EMA50Gate(t *testing.T) {
	for _, ema50 := range []float64{4005, 0} {
		h1 := baseH1()
		h1.EMA50 = ema50
		if r := evalBuy(h1, 4000); r.signal != config.SignalHold {
			t.Errorf("EMA50 %v: got %s, want HOLD", ema50, r.signal)
		}
	}
}

// 5 Nov 20:05: H1 EMA9-EMA21 gap 8.4 after peaking at 26 (ratio 0.32).
func TestH1EMAGapFading(t *testing.T) {
	h1 := baseH1()
	h1.EMAFast, h1.EMASlow, h1.RecentEMAGapMax = 4099.4, 4091.0, 26
	if r := evalBuy(h1, 4000); r.signal != config.SignalHold {
		t.Errorf("gap 0.32 of peak: got %s, want HOLD", r.signal)
	}
	h1.RecentEMAGapMax = 13 // gap 8.4/13 = 0.65
	h1.RecentHigh = 4001
	if r := evalBuy(h1, 4000); r.signal != config.SignalBuy {
		t.Errorf("gap 0.65 of peak: got %s (%s), want BUY", r.signal, r.reason)
	}
}

// 5 Nov 20:05: entry 4118.97, 6h high 4138.44, H1 ATR 26.25 → 0.74 ATR below the high.
func TestH1Bounce(t *testing.T) {
	h1 := baseH1()
	h1.ATR, h1.RecentHigh = 26.25, 4138.44
	if r := evalBuy(h1, 4118.97); r.signal != config.SignalHold {
		t.Errorf("0.74 ATR below high: got %s, want HOLD", r.signal)
	}
	h1.RecentHigh = 4125 // 0.23 ATR below
	if r := evalBuy(h1, 4118.97); r.signal != config.SignalBuy {
		t.Errorf("0.23 ATR below high: got %s (%s), want BUY", r.signal, r.reason)
	}
}

func TestSellMirrorsBounceAndGap(t *testing.T) {
	s := New()
	h1 := indicator.MarketState{
		IsWarmedUp: true, Regime: config.TrendingDown, ADX: 30, ATR: 20,
		EMAFast: 3990, EMASlow: 4000, EMA50: 4010,
		RecentHigh: 4050, RecentLow: 3995, RecentEMAGapMax: -2, RecentEMAGapMin: -12,
	}
	m5 := indicator.MarketState{IsWarmedUp: true, EMAFast: 99, EMASlow: 100}
	m15 := indicator.MarketState{IsWarmedUp: true, ATR: 5, BarTime: 1000}
	eval := func(price float64) string {
		var sig string
		for i, reg := range []string{config.Ranging, config.Ranging, config.TrendingDown} {
			m5.BarTime, m5.Regime = int64(i+1)+int64(price), reg
			sig = s.Evaluate(map[string]indicator.MarketState{config.PeriodH1: h1, config.PeriodM5: m5, config.PeriodM15: m15}, price, 0.01).Signal
		}
		return sig
	}
	if got := eval(4000); got != config.SignalSell {
		t.Errorf("sell near recent low: got %s, want SELL", got)
	}
	s = New()
	if got := eval(4010); got != config.SignalHold {
		t.Errorf("sell 0.75 ATR above recent low: got %s, want HOLD", got)
	}
}

func TestEntryRulesRecorded(t *testing.T) {
	r := evalBuy(baseH1(), 4000)
	if r.signal != config.SignalBuy {
		t.Fatalf("got %s (%s), want BUY", r.signal, r.reason)
	}
	want := map[string][2]float64{
		"h1_ema_gap_ratio":  {10.0 / 12, minH1EMAGapRatio},
		"h1_bounce_atr":     {5.0 / 20, maxH1BounceATR},
		"h1_adx":            {30, minH1ADXForTrend},
		"h1_ema21_vs_ema50": {4000, 3990},
		"m5_regime_streak":  {1, maxFreshM5RegimeStreak},
	}
	for name, w := range want {
		got, ok := r.rules[name]
		if !ok || got.Why == "" {
			t.Errorf("%s missing or has no description", name)
			continue
		}
		if math.Abs(got.Value-w[0]) > 1e-3 || got.Limit != w[1] {
			t.Errorf("%s = %v/%v, want %v/%v", name, got.Value, got.Limit, w[0], w[1])
		}
	}
}
