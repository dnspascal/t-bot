package trendpullback

import (
	"testing"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
)

// Uptrend with ADX 35, H1 ATR 20, recent high 4040: price 4020 is a 1.0 ATR pullback.
func baseH1() indicator.MarketState {
	return indicator.MarketState{
		IsWarmedUp: true, Regime: config.TrendingUp, ADX: 35, ATR: 20,
		EMAFast: 4010, EMASlow: 4000, EMA50: 3990,
		RecentHigh: 4040, RecentLow: 3950, RecentEMAGapMax: 12, RecentEMAGapMin: 4,
	}
}

// Feeds 3 M5 bars (ranging, ranging, trending_up) so the freshness gate passes, returns the last signal.
func evalBuy(s *TrendPullback, h1 indicator.MarketState, price, m15ATR float64) (string, string, float64) {
	m5 := indicator.MarketState{IsWarmedUp: true, EMAFast: 101, EMASlow: 100}
	m15 := indicator.MarketState{IsWarmedUp: true, ATR: m15ATR, BarTime: 1000}
	var sig, why string
	var sl float64
	for i, reg := range []string{config.Ranging, config.Ranging, config.TrendingUp} {
		m5.BarTime, m5.Regime = int64(i+1), reg
		r := s.Evaluate(map[string]indicator.MarketState{config.PeriodH1: h1, config.PeriodM5: m5, config.PeriodM15: m15}, price, 0.1)
		sig, why, sl = r.Signal, r.Reason, r.SLPrice
	}
	return sig, why, sl
}

func TestPullbackBuys(t *testing.T) {
	sig, why, sl := evalBuy(New(), baseH1(), 4020, 10)
	if sig != config.SignalBuy {
		t.Fatalf("1.0 ATR pullback: got %s (%s), want BUY", sig, why)
	}
	if sl != 4005 {
		t.Errorf("SL = %v, want 4005 (1.5 x M15 ATR 10 below)", sl)
	}
}

func TestPullbackWindow(t *testing.T) {
	for _, tc := range []struct {
		price float64
		want  string
	}{{4030, config.SignalHold}, {4025, config.SignalBuy}, {4010, config.SignalBuy}, {4005, config.SignalHold}} {
		if sig, why, _ := evalBuy(New(), baseH1(), tc.price, 10); sig != tc.want {
			t.Errorf("price %v (%.2f ATR off high): got %s (%s), want %s", tc.price, (4040-tc.price)/20, sig, why, tc.want)
		}
	}
}

func TestWeakTrendHolds(t *testing.T) {
	h1 := baseH1()
	h1.ADX = 25
	if sig, _, _ := evalBuy(New(), h1, 4020, 10); sig != config.SignalHold {
		t.Errorf("ADX 25: got %s, want HOLD", sig)
	}
}

func TestStopCap(t *testing.T) {
	if sig, why, _ := evalBuy(New(), baseH1(), 4020, MaxStopDistance/1.5); sig != config.SignalBuy {
		t.Errorf("stop exactly at the cap: got %s (%s), want BUY", sig, why)
	}
	if sig, _, _ := evalBuy(New(), baseH1(), 4020, MaxStopDistance/1.5+0.1); sig != config.SignalHold {
		t.Errorf("stop over the cap: got %s, want HOLD", sig)
	}
}

func TestNoGapSkipsGapCheck(t *testing.T) {
	h1 := baseH1()
	h1.RecentEMAGapMax = 40 // gap 10 is 0.25 of its peak: trend fading
	if sig, _, _ := evalBuy(New(), h1, 4020, 10); sig != config.SignalHold {
		t.Errorf("live version with fading gap: got %s, want HOLD", sig)
	}
	if sig, why, _ := evalBuy(NewNoGap(), h1, 4020, 10); sig != config.SignalBuy {
		t.Errorf("nogap version with fading gap: got %s (%s), want BUY", sig, why)
	}
	if New().Name() != "trend_pullback" || NewNoGap().Name() != "trend_pullback_nogap" {
		t.Error("unexpected strategy names")
	}
}
