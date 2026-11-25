package trendfollowbigday

import (
	"math"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
	trendfollow "github.com/denismgaya/t-bot/internal/strategy/trend_follow"
)

// trend_follow's entries, taken only on big days: today's (UTC) range so far must be >= K x the average range of
// the previous 20 days. Backtested as tfbig_utc_k1.75 (research/studies/2026-11-25-trend-follow-big-days).
const (
	K        = 1.75
	ADRDays  = 20
	seedBars = 600 // H1 bars loaded at startup, ~25 days
)

type TrendFollowBigDay struct {
	inner   *trendfollow.TrendFollow
	day     string
	hi, lo  float64
	past    []float64
	lastBar int64
}

func New() *TrendFollowBigDay { return &TrendFollowBigDay{inner: trendfollow.New()} }

func (s *TrendFollowBigDay) Name() string           { return "trend_follow_bigday" }
func (s *TrendFollowBigDay) UsesTrendWatcher() bool { return true }

// OnClosed implements strategy.OutcomeAware for the inner trend_follow (its cooldowns).
func (s *TrendFollowBigDay) OnClosed(side, closeReason string, closeTime time.Time) {
	s.inner.OnClosed(side, closeReason, closeTime)
}

// SeedCount implements strategy.H1Seeder.
func (s *TrendFollowBigDay) SeedCount() int { return seedBars }

// SeedH1 loads past daily ranges from H1 bars (oldest first) so the average is known right after a restart.
func (s *TrendFollowBigDay) SeedH1(bars []strategy.Bar) {
	for _, b := range bars {
		s.observe(b.OpenTime, b.High, b.Low)
	}
}

func (s *TrendFollowBigDay) observe(openTime int64, high, low float64) {
	d := time.Unix(openTime, 0).UTC().Format(time.DateOnly)
	if d != s.day {
		if s.day != "" && s.hi > s.lo {
			s.past = append(s.past, s.hi-s.lo)
			if len(s.past) > ADRDays {
				s.past = s.past[1:]
			}
		}
		s.day, s.hi, s.lo = d, high, low
		return
	}
	s.hi, s.lo = math.Max(s.hi, high), math.Min(s.lo, low)
}

// DayRatio is today's range so far over the 20-day average (0 until 20 past days are known).
func (s *TrendFollowBigDay) DayRatio() float64 {
	if len(s.past) < ADRDays {
		return 0
	}
	adr := 0.0
	for _, v := range s.past {
		adr += v
	}
	adr /= float64(len(s.past))
	if adr <= 0 {
		return 0
	}
	return (s.hi - s.lo) / adr
}

func (s *TrendFollowBigDay) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
	if m5 := states[config.PeriodM5]; m5.IsWarmedUp && m5.BarTime != s.lastBar {
		s.lastBar = m5.BarTime
		s.observe(m5.BarTime, m5.High, m5.Low)
	}
	r := s.inner.Evaluate(states, price, pipSize) // always evaluated, so trend_follow's own state stays as live
	if r.Signal != config.SignalBuy && r.Signal != config.SignalSell {
		return r
	}
	ratio := s.DayRatio()
	if ratio == 0 {
		return strategy.EntryResult{Signal: config.SignalHold, Reason: "average daily range not known yet"}
	}
	if ratio < K {
		return strategy.EntryResult{Signal: config.SignalHold, Reason: "not a big day: range so far below 1.75x the 20-day average"}
	}
	if r.Rules == nil {
		r.Rules = map[string]strategy.RuleCheck{}
	}
	r.Rules["day_range_vs_adr20"] = strategy.RuleCheck{Value: math.Round(ratio*100) / 100, Limit: K,
		Why: "today's UTC range so far / average range of the previous 20 days; must be >= limit (big day)"}
	return r
}
