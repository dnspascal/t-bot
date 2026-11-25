package main

import (
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
	trendfollow "github.com/denismgaya/t-bot/internal/strategy/trend_follow"
	trendfollowbigday "github.com/denismgaya/t-bot/internal/strategy/trend_follow_bigday"
)

// Research: trend_follow (or trend_follow_bigday) with entries blocked by the H1 regime at entry. The inner strategy is
// evaluated on every bar, so its own state stays as live.
type regimeFilter struct {
	name  string
	inner strategy.Strategy
	block func(signal, h1Regime string) bool
}

func (s *regimeFilter) Name() string           { return s.name }
func (s *regimeFilter) UsesTrendWatcher() bool { return true }

// outcomeRegime forwards closes to the inner strategy (trend_follow's cooldowns).
type outcomeRegime struct{ regimeFilter }

func (s *outcomeRegime) OnClosed(side, reason string, at time.Time) {
	if a, ok := s.inner.(strategy.OutcomeAware); ok {
		a.OnClosed(side, reason, at)
	}
}

func (s *regimeFilter) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
	r := s.inner.Evaluate(states, price, pipSize)
	if (r.Signal == config.SignalBuy || r.Signal == config.SignalSell) && s.block(r.Signal, states[config.PeriodH1].Regime) {
		return strategy.EntryResult{Signal: config.SignalHold, Reason: "blocked by H1 regime filter"}
	}
	return r
}

func init() {
	rules := map[string]func(sig, reg string) bool{
		"noup":         func(sig, reg string) bool { return sig == config.SignalBuy && reg == config.TrendingUp },
		"nodown":       func(sig, reg string) bool { return sig == config.SignalSell && reg == config.TrendingDown },
		"onlybreakout": func(sig, reg string) bool { return reg != config.Breakout },
	}
	for k, f := range rules {
		k, f := k, f
		strategies["tf_"+k] = func() strategy.Strategy {
			return &outcomeRegime{regimeFilter{name: "tf_" + k, inner: trendfollow.New(), block: f}}
		}
	}
	strategies["bigday_noup"] = func() strategy.Strategy {
		return &outcomeRegime{regimeFilter{name: "bigday_noup", inner: trendfollowbigday.New(), block: rules["noup"]}}
	}
}
