package trendfollowbigday

import (
	"testing"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

// 21 past days (Nov 10-30) with a $10 range each.
func seeded() *TrendFollowBigDay {
	s := New()
	var bars []strategy.Bar
	for d := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC); d.Day() != 1; d = d.AddDate(0, 0, 1) {
		bars = append(bars, strategy.Bar{OpenTime: d.Unix(), High: 4010, Low: 4000})
	}
	s.SeedH1(bars)
	return s
}

// The same trend_follow BUY setup as its own tests: M5 ranging, ranging, then trending up; today's M5 range is set by hi.
func evalBuy(s *TrendFollowBigDay, hi float64) (string, string) {
	h1 := indicator.MarketState{IsWarmedUp: true, Regime: config.TrendingUp, ADX: 30, ATR: 20,
		EMAFast: 4010, EMASlow: 4000, EMA50: 3990, RecentHigh: 4005, RecentLow: 3950, RecentEMAGapMax: 12, RecentEMAGapMin: 4}
	m15 := indicator.MarketState{IsWarmedUp: true, ATR: 5, BarTime: 1000}
	m5 := indicator.MarketState{IsWarmedUp: true, EMAFast: 101, EMASlow: 100, High: hi, Low: 4000}
	start := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)
	var r strategy.EntryResult
	for i, reg := range []string{config.Ranging, config.Ranging, config.TrendingUp} {
		m5.BarTime, m5.Regime = start.Add(time.Duration(i)*5*time.Minute).Unix(), reg
		r = s.Evaluate(map[string]indicator.MarketState{config.PeriodH1: h1, config.PeriodM5: m5, config.PeriodM15: m15}, 4000, 0.01)
	}
	return r.Signal, r.Reason
}

func TestBigDayLetsTrendFollowThrough(t *testing.T) {
	s := seeded()
	sig, why := evalBuy(s, 4020) // today $20 vs average $10: 2.0x
	if sig != config.SignalBuy {
		t.Fatalf("big day: got %s (%s), want BUY", sig, why)
	}
	if r := s.DayRatio(); r != 2 {
		t.Errorf("DayRatio = %v, want 2", r)
	}
}

func TestNormalDayHolds(t *testing.T) {
	sig, why := evalBuy(seeded(), 4015) // 1.5x, below 1.75
	if sig != config.SignalHold || why != "not a big day: range so far below 1.75x the 20-day average" {
		t.Errorf("1.5x day: got %s (%s), want HOLD not a big day", sig, why)
	}
}

func TestNoHistoryHolds(t *testing.T) {
	sig, why := evalBuy(New(), 4100)
	if sig != config.SignalHold || why != "average daily range not known yet" {
		t.Errorf("no seed: got %s (%s), want HOLD until 20 days are known", sig, why)
	}
}
