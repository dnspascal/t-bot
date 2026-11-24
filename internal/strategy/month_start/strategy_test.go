package monthstart

import (
	"testing"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
)

func eval(s *MonthStart, barOpenNY string, h1ATR float64) (string, float64) {
	t, err := time.ParseInLocation("2006-01-02 15:04", barOpenNY, newYork)
	if err != nil {
		panic(err)
	}
	r := s.Evaluate(map[string]indicator.MarketState{
		config.PeriodM5: {IsWarmedUp: true, BarTime: t.Unix()},
		config.PeriodH1: {IsWarmedUp: true, ATR: h1ATR},
	}, 4000, 0.1)
	return r.Signal, r.SLPrice
}

func TestFirstThreeWeekdays(t *testing.T) {
	for _, tc := range []struct {
		bar, want string
	}{
		{"2026-11-30 18:00", config.SignalBuy},  // Monday evening opens Tue Dec 1: weekday 1
		{"2026-12-01 18:00", config.SignalBuy},  // Wed Dec 2: 2
		{"2026-12-02 18:00", config.SignalBuy},  // Thu Dec 3: 3
		{"2026-12-03 18:00", config.SignalHold}, // Fri Dec 4: 4
		{"2026-10-30 18:00", config.SignalHold}, // Friday evening: no Saturday session
		{"2026-11-01 18:00", config.SignalBuy},  // Sunday evening opens Mon Nov 2: weekday 1
		{"2026-11-04 18:00", config.SignalHold}, // Thu Nov 5: 4
		{"2026-12-01 18:05", config.SignalHold}, // not the first bar after the re-open
		{"2026-12-01 13:00", config.SignalHold},
	} {
		if sig, _ := eval(New(), tc.bar, 10); sig != tc.want {
			t.Errorf("%s NY: got %s, want %s", tc.bar, sig, tc.want)
		}
	}
}

func TestStopOncePerSessionAndCap(t *testing.T) {
	s := New()
	sig, sl := eval(s, "2026-11-30 18:00", 10)
	if sig != config.SignalBuy || sl != 3990 {
		t.Fatalf("got %s sl=%v, want BUY sl=3990", sig, sl)
	}
	if sig, _ := eval(s, "2026-11-30 18:00", 10); sig != config.SignalHold {
		t.Errorf("same session twice: got %s, want HOLD", sig)
	}
	if sig, _ := eval(New(), "2026-11-30 18:00", MaxStopDistance+0.5); sig != config.SignalHold {
		t.Errorf("stop over the cap: got %s, want HOLD", sig)
	}
	if New().MaxHold() != 22*time.Hour+45*time.Minute {
		t.Errorf("MaxHold = %v", New().MaxHold())
	}
}
