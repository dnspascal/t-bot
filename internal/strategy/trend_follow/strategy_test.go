package trendfollow

import (
	"testing"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
)

// Feeds 3 M5 bars (ranging, ranging, trending_up) so the freshness gate passes, returns the last signal.
func evalBuySetup(h1EMA50 float64) string {
	s := New()
	h1 := indicator.MarketState{IsWarmedUp: true, Regime: config.TrendingUp, ADX: 30, EMAFast: 4010, EMASlow: 4000, EMA50: h1EMA50}
	m5 := indicator.MarketState{IsWarmedUp: true, EMAFast: 101, EMASlow: 100}
	m15 := indicator.MarketState{IsWarmedUp: true, ATR: 5, BarTime: 1000}
	var sig string
	for i, reg := range []string{config.Ranging, config.Ranging, config.TrendingUp} {
		m5.BarTime, m5.Regime = int64(i+1), reg
		states := map[string]indicator.MarketState{config.PeriodH1: h1, config.PeriodM5: m5, config.PeriodM15: m15}
		sig = s.Evaluate(states, 4000, 0.01).Signal
	}
	return sig
}

func TestH1EMA50Gate(t *testing.T) {
	if got := evalBuySetup(3990); got != config.SignalBuy {
		t.Errorf("EMA21 above EMA50: got %s, want BUY", got)
	}
	if got := evalBuySetup(4005); got != config.SignalHold {
		t.Errorf("EMA21 below EMA50: got %s, want HOLD", got)
	}
	if got := evalBuySetup(0); got != config.SignalHold {
		t.Errorf("EMA50 not ready: got %s, want HOLD", got)
	}
}
