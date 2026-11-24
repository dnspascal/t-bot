package main

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
	sessionopen "github.com/denismgaya/t-bot/internal/strategy/session_open"
	"github.com/denismgaya/t-bot/internal/strategy/sweep"
)

// Research: <name>_gate skips entries the live invalidation watcher would already close (2+ of EMA cross, RSI, momentum against).
type gated struct{ inner strategy.Strategy }

func (g *gated) Name() string           { return g.inner.Name() + "_gate" }
func (g *gated) UsesTrendWatcher() bool { return g.inner.UsesTrendWatcher() }
func (g *gated) OnClosed(side, reason string, at time.Time) {
	if a, ok := g.inner.(strategy.OutcomeAware); ok {
		a.OnClosed(side, reason, at)
	}
}
func (g *gated) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
	r := g.inner.Evaluate(states, price, pipSize)
	if (r.Signal == config.SignalBuy || r.Signal == config.SignalSell) && threeVotesAgainst(states[config.PeriodM5], r.Signal) >= 2 {
		return strategy.EntryResult{Signal: config.SignalHold}
	}
	return r
}

func threeVotesAgainst(ms indicator.MarketState, side string) int {
	return reversalSignals(ms, side) - regimeAgainst(ms, side)
}

// Research: session_momentum with extra conditions. Flags: h1 = H1 EMA fast/slow agree, m5 = M5 EMA cross and RSI agree,
// two = previous M5 candle has the same colour, late = window shifted 30 min later (London 08:30, NY 09:30 = US stock open).
type smVariant struct {
	flags       string
	lastSession string
	prevBody    float64
	prevBarTime int64
	lastBarTime int64
	lastBarBody float64
}

func init() {
	strategies["session_open"] = func() strategy.Strategy { return sessionopen.New() }
	strategies["sweep_gate"] = func() strategy.Strategy { return &gated{sweep.New()} }
	for _, f := range []string{"base", "h1", "m5", "two", "late", "h1_m5", "h1_two", "h1_m5_two", "late_h1", "late_h1_m5", "late_h1_m5_two"} {
		f := f
		strategies["smv_"+f] = func() strategy.Strategy { return &smVariant{flags: f} }
	}
}

func (s *smVariant) Name() string           { return "smv_" + s.flags }
func (s *smVariant) UsesTrendWatcher() bool { return false }
func (s *smVariant) has(f string) bool {
	for _, x := range strings.Split(s.flags, "_") {
		if x == f {
			return true
		}
	}
	return false
}

func (s *smVariant) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
	hold := strategy.EntryResult{Signal: config.SignalHold}
	m5, m15, h1 := states[config.PeriodM5], states[config.PeriodM15], states[config.PeriodH1]
	if !m5.IsWarmedUp || m5.ATR <= 0 || !m15.IsWarmedUp || m15.ATR <= 0 {
		return hold
	}
	body := m5.Close - m5.Open
	if m5.BarTime != s.lastBarTime {
		s.prevBody, s.prevBarTime = s.lastBarBody, s.lastBarTime
		s.lastBarTime, s.lastBarBody = m5.BarTime, body
	}
	t := time.Unix(m5.BarTime, 0).UTC()
	if s.has("late") {
		t = t.Add(-30 * time.Minute)
	}
	session := config.SessionOpen(t)
	if session == "" {
		return hold
	}
	key := fmt.Sprintf("%s-%s", session, t.Format("2006-01-02"))
	if s.lastSession == key || math.Abs(body) < 0.4*m5.ATR {
		return hold
	}
	dir := config.SignalSell
	if body > 0 {
		dir = config.SignalBuy
	}
	buy := dir == config.SignalBuy
	if h1.IsWarmedUp && h1.EMA50 > 0 && (buy && price < h1.EMA50 || !buy && price > h1.EMA50) {
		return hold
	}
	if s.has("h1") && (!h1.IsWarmedUp || buy && h1.EMAFast <= h1.EMASlow || !buy && h1.EMAFast >= h1.EMASlow) {
		return hold
	}
	if s.has("m5") && threeVotesAgainst(m5, dir) > 0 {
		return hold
	}
	if s.has("two") && (s.prevBarTime != m5.BarTime-300 || buy && s.prevBody <= 0 || !buy && s.prevBody >= 0) {
		return hold
	}
	atr := m15.ATR
	sl, tp := price-atr, price+2*atr
	if !buy {
		sl, tp = price+atr, price-2*atr
	}
	s.lastSession = key
	return strategy.EntryResult{Signal: dir, SLPrice: sl, TPPrice: tp, SLPips: atr / pipSize, TPPips: 2 * atr / pipSize, ATR: atr, Tier: config.TierNormal}
}

// Research: <name>_cool<N> blocks same-side entries for N minutes after a stop loss; _coolany<N> after any invalidated close.
type coolAfterLoss struct {
	inner strategy.Strategy
	mins  int
	any   bool
	until map[string]int64
}

func init() {
	for _, base := range []string{"ema_pullback", "trend_follow"} {
		for _, m := range []int{60, 120, 240, 480} {
			base, m := base, m
			mk := strategies[base]
			strategies[fmt.Sprintf("%s_cool%d", base, m)] = func() strategy.Strategy { return &coolAfterLoss{inner: mk(), mins: m} }
			strategies[fmt.Sprintf("%s_coolany%d", base, m)] = func() strategy.Strategy { return &coolAfterLoss{inner: mk(), mins: m, any: true} }
		}
	}
}

func (c *coolAfterLoss) Name() string           { return fmt.Sprintf("%s_cool%d", c.inner.Name(), c.mins) }
func (c *coolAfterLoss) UsesTrendWatcher() bool { return c.inner.UsesTrendWatcher() }
func (c *coolAfterLoss) OnClosed(side, reason string, at time.Time) {
	if a, ok := c.inner.(strategy.OutcomeAware); ok {
		a.OnClosed(side, reason, at)
	}
	if reason == strategy.CloseReasonSLHit || c.any && strategy.ClassifyCloseReason(reason) == strategy.CloseInvalidated {
		if c.until == nil {
			c.until = map[string]int64{}
		}
		c.until[side] = at.Unix() + int64(c.mins)*60
	}
}
func (c *coolAfterLoss) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
	r := c.inner.Evaluate(states, price, pipSize)
	if (r.Signal == config.SignalBuy || r.Signal == config.SignalSell) && states[config.PeriodM5].BarTime+300 < c.until[r.Signal] {
		return strategy.EntryResult{Signal: config.SignalHold}
	}
	return r
}
