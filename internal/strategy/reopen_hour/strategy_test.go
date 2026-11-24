package reopenhour

import (
	"testing"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
)

func eval(s *ReopenHour, barOpen time.Time, h1ATR float64) (string, float64, float64) {
	states := map[string]indicator.MarketState{
		config.PeriodM5: {IsWarmedUp: true, BarTime: barOpen.Unix()},
		config.PeriodH1: {IsWarmedUp: true, ATR: h1ATR},
	}
	r := s.Evaluate(states, 4000, 0.1)
	return r.Signal, r.SLPrice, r.TPPrice
}

func TestBuysFirstBarAfterReopenSummerAndWinter(t *testing.T) {
	for _, tc := range []struct {
		name string
		bar  time.Time
	}{
		{"summer, NY 18:00 EDT = 22:00 UTC", time.Date(2026, 7, 28, 22, 0, 0, 0, time.UTC)},
		{"winter, NY 18:00 EST = 23:00 UTC", time.Date(2026, 11, 24, 23, 0, 0, 0, time.UTC)},
	} {
		sig, sl, tp := eval(New(), tc.bar, 10)
		if sig != config.SignalBuy || sl != 3990 || tp != 4030 {
			t.Errorf("%s: got %s sl=%v tp=%v, want BUY sl=3990 tp=4030", tc.name, sig, sl, tp)
		}
	}
}

func TestHoldsOutsideTheReopenBar(t *testing.T) {
	for _, bar := range []time.Time{
		time.Date(2026, 7, 28, 22, 5, 0, 0, time.UTC),  // second bar
		time.Date(2026, 7, 28, 21, 55, 0, 0, time.UTC), // before the break
		time.Date(2026, 11, 24, 22, 0, 0, 0, time.UTC), // 22:00 UTC is 17:00 NY in winter
		time.Date(2026, 7, 31, 22, 0, 0, 0, time.UTC),  // Friday evening NY: no re-open
	} {
		if sig, _, _ := eval(New(), bar, 10); sig != config.SignalHold {
			t.Errorf("%s: got %s, want HOLD", bar, sig)
		}
	}
}

func TestOncePerReopenAndStopCap(t *testing.T) {
	s, bar := New(), time.Date(2026, 7, 28, 22, 0, 0, 0, time.UTC)
	if sig, _, _ := eval(s, bar, 10); sig != config.SignalBuy {
		t.Fatalf("first evaluation: got %s, want BUY", sig)
	}
	if sig, _, _ := eval(s, bar, 10); sig != config.SignalHold {
		t.Errorf("same re-open again: got %s, want HOLD", sig)
	}
	if sig, _, _ := eval(New(), bar, MaxStopDistance+1); sig != config.SignalHold {
		t.Errorf("stop over the cap: got %s, want HOLD", sig)
	}
	if New().MaxHold() != time.Hour {
		t.Errorf("MaxHold = %v, want 1h", New().MaxHold())
	}
}
