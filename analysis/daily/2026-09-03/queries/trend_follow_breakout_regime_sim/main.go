package main

// trend_follow's H1-regime switch only handles trending_up/trending_down;
// "breakout" (a real, distinct, usually-directional regime — confirmed
// today: EMA fast>slow held throughout all 6 breakout-labeled H1 bars,
// matching the same alignment as trending_up bars) falls into the generic
// "H1 regime unclear" hold, same as a genuinely ambiguous reading. Today
// alone, breakout regime covered 6 of 11 H1 bars and the bulk of the day's
// actual move (4382->4431).
//
// This replays the strategy's REAL entry gate exactly (M5 regime-streak
// state machine included, walked chronologically) against XAUUSD's full
// history, comparing:
//
//   current:      today's actual logic (breakout regime -> always hold)
//   with_breakout: breakout regime allowed, direction inferred from
//                  M5 EMA fast vs slow (fast>slow -> BUY, else SELL),
//                  otherwise identical gate (EMA alignment, M5 regime
//                  match, freshness, ADX floor)
//
// using the strategy's own real SL/TP math (SL=1.5x M15 ATR,
// TP=2.5x M15 ATR) replayed against real M1 price data. Reports the
// baseline (matches current production logic) and the trades ADDED by
// allowing breakout regime, in isolation, so it's clear whether the
// added trades are themselves net positive — not just folded into a
// combined total that could hide a net-negative addition.
//
// Run: go run . <postgres-url>
import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	maxFreshM5RegimeStreak      = 2
	minTrackedBarsBeforeTrustng = maxFreshM5RegimeStreak + 1
	slATRMult                   = 1.5
	tpATRMult                   = 2.5
	adxFloor                    = 20.0
	simWindowHrs                = 24
	dollarPerPt                 = 1.0
)

type H1State struct {
	BarTime          time.Time
	Regime           string
	EMAFast, EMASlow float64
	ADX              float64
}
type M5State struct {
	BarTime          time.Time
	Regime           string
	EMAFast, EMASlow float64
	Close            float64
}
type M15State struct {
	BarTime time.Time
	ATR     float64
}
type Candle struct {
	BarTime         time.Time
	Open, High, Low float64
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

type result struct {
	name                     string
	n, win, lose, unresolved int
	pnl                      float64
}

func (r *result) record(outcome int, entry, exitPrice float64) {
	r.n++
	switch outcome {
	case 1:
		r.win++
	case -1:
		r.lose++
	default:
		r.unresolved++
	}
	r.pnl += (exitPrice - entry) * dollarPerPt
}

func (r result) print() {
	fmt.Printf("%-20s n:%-5d win:%-4d lose:%-4d unresolved:%-4d winrate:%5.1f%%  pnl:%9.2f\n",
		r.name, r.n, r.win, r.lose, r.unresolved, pct(r.win, r.win+r.lose), r.pnl)
}
func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return 100 * float64(n) / float64(d)
}

func simulate(candles []Candle, startIdx int, start time.Time, entry, sl, tp float64, isBuy bool) int {
	endTime := start.Add(simWindowHrs * time.Hour)
	for i := startIdx; i < len(candles); i++ {
		c := candles[i]
		if c.BarTime.Before(start) {
			continue
		}
		if c.BarTime.After(endTime) {
			break
		}
		var tpHit, slHit bool
		if isBuy {
			tpHit, slHit = c.High >= tp, c.Low <= sl
		} else {
			tpHit, slHit = c.Low <= tp, c.High >= sl
		}
		if slHit && tpHit {
			return -1
		} else if tpHit {
			return 1
		} else if slHit {
			return -1
		}
	}
	return 0
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: trend_follow_breakout_regime_sim <postgres-url>")
		os.Exit(1)
	}
	db, err := sql.Open("pgx", os.Args[1])
	must(err)
	defer db.Close()
	ctx := context.Background()

	h1 := []H1State{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT ms.bar_time, COALESCE(ms.regime,''), COALESCE(ms.ema_fast,0), COALESCE(ms.ema_slow,0), COALESCE(ms.adx,0)
			FROM market_states ms JOIN symbols s ON s.id = ms.symbol_id
			WHERE s.symbol='XAUUSD' AND ms.period='H1' ORDER BY ms.bar_time ASC`)
		must(err)
		for rows.Next() {
			var st H1State
			must(rows.Scan(&st.BarTime, &st.Regime, &st.EMAFast, &st.EMASlow, &st.ADX))
			h1 = append(h1, st)
		}
		rows.Close()
	}

	m5 := []M5State{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT ms.bar_time, COALESCE(ms.regime,''), COALESCE(ms.ema_fast,0), COALESCE(ms.ema_slow,0), COALESCE(ms.close,0)
			FROM market_states ms JOIN symbols s ON s.id = ms.symbol_id
			WHERE s.symbol='XAUUSD' AND ms.period='M5' ORDER BY ms.bar_time ASC`)
		must(err)
		for rows.Next() {
			var st M5State
			must(rows.Scan(&st.BarTime, &st.Regime, &st.EMAFast, &st.EMASlow, &st.Close))
			m5 = append(m5, st)
		}
		rows.Close()
	}

	m15 := []M15State{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT ms.bar_time, COALESCE(ms.atr,0)
			FROM market_states ms JOIN symbols s ON s.id = ms.symbol_id
			WHERE s.symbol='XAUUSD' AND ms.period='M15' ORDER BY ms.bar_time ASC`)
		must(err)
		for rows.Next() {
			var st M15State
			must(rows.Scan(&st.BarTime, &st.ATR))
			m15 = append(m15, st)
		}
		rows.Close()
	}

	candles := []Candle{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT c.bar_time, c.open, c.high, c.low
			FROM candles c JOIN symbols s ON s.id = c.symbol_id
			WHERE s.symbol='XAUUSD' AND c.period='M1' ORDER BY c.bar_time ASC`)
		must(err)
		for rows.Next() {
			var c Candle
			must(rows.Scan(&c.BarTime, &c.Open, &c.High, &c.Low))
			candles = append(candles, c)
		}
		rows.Close()
	}
	fmt.Fprintf(os.Stderr, "loaded %d H1, %d M5, %d M15 states, %d M1 candles\n", len(h1), len(m5), len(m15), len(candles))

	h1Times := make([]time.Time, len(h1))
	for i, h := range h1 {
		h1Times[i] = h.BarTime
	}
	m15Times := make([]time.Time, len(m15))
	for i, m := range m15 {
		m15Times[i] = m.BarTime
	}
	nearest := func(bt time.Time, times []time.Time) int {
		i := sort.Search(len(times), func(i int) bool { return times[i].After(bt) })
		return i - 1
	}
	candleIdxFor := func(bt time.Time) int {
		return sort.Search(len(candles), func(i int) bool { return !candles[i].BarTime.Before(bt) })
	}

	current := &result{name: "current (baseline)"}
	added := &result{name: "ADDED by breakout+EMA"}
	combined := &result{name: "combined total"}
	weekly := map[string]*result{}

	// Replicate the real M5-regime-streak state machine, walked chronologically.
	var lastM5Regime string
	var lastM5BarTime int64
	m5RegimeStreak := 0
	unseenBarsTracked := 0

	for _, bar := range m5 {
		barUnix := bar.BarTime.Unix()
		if barUnix != lastM5BarTime {
			if bar.Regime == lastM5Regime {
				m5RegimeStreak++
			} else {
				lastM5Regime = bar.Regime
				m5RegimeStreak = 1
			}
			lastM5BarTime = barUnix
			if unseenBarsTracked < minTrackedBarsBeforeTrustng {
				unseenBarsTracked++
			}
		}

		hi := nearest(bar.BarTime, h1Times)
		if hi < 0 || bar.BarTime.Sub(h1[hi].BarTime) > 65*time.Minute {
			continue
		}
		h := h1[hi]
		if h.Regime == "ranging" {
			continue
		}

		mi := nearest(bar.BarTime, m15Times)
		if mi < 0 || bar.BarTime.Sub(m15[mi].BarTime) > 20*time.Minute || m15[mi].ATR <= 0 {
			continue
		}
		atr := m15[mi].ATR

		tryEntry := func(dir string, targets ...*result) {
			isBuy := dir == "BUY"
			if isBuy && bar.EMAFast < bar.EMASlow {
				return
			}
			if !isBuy && bar.EMAFast > bar.EMASlow {
				return
			}
			if isBuy && bar.Regime != "trending_up" {
				return
			}
			if !isBuy && bar.Regime != "trending_down" {
				return
			}
			if unseenBarsTracked < minTrackedBarsBeforeTrustng {
				return
			}
			if m5RegimeStreak > maxFreshM5RegimeStreak {
				return
			}
			if h.ADX < adxFloor {
				return
			}

			entry := bar.Close
			var sl, tp float64
			if isBuy {
				sl, tp = entry-slATRMult*atr, entry+tpATRMult*atr
			} else {
				sl, tp = entry+slATRMult*atr, entry-tpATRMult*atr
			}
			idx := candleIdxFor(bar.BarTime)
			outcome := simulate(candles, idx, bar.BarTime, entry, sl, tp, isBuy)
			exitPrice := entry
			if outcome == 1 {
				exitPrice = tp
			} else if outcome == -1 {
				exitPrice = sl
			}
			signedExit := exitPrice
			if !isBuy {
				signedExit = 2*entry - exitPrice
			}
			for _, r := range targets {
				r.record(outcome, entry, signedExit)
			}
			combined.record(outcome, entry, signedExit)
		}

		switch h.Regime {
		case "trending_up":
			tryEntry("BUY", current)
		case "trending_down":
			tryEntry("SELL", current)
		case "breakout":
			wk := bar.BarTime.Truncate(7 * 24 * time.Hour).Format("2006-01-02")
			if weekly[wk] == nil {
				weekly[wk] = &result{name: "  week of " + wk}
			}
			dir := "SELL"
			if bar.EMAFast > bar.EMASlow {
				dir = "BUY"
			}
			tryEntry(dir, added, weekly[wk])
		}
	}

	fmt.Println()
	current.print()
	added.print()
	combined.print()

	fmt.Println("\nADDED (breakout+EMA) trades by week — checking it's not one lucky week:")
	weeks := make([]string, 0, len(weekly))
	for wk := range weekly {
		weeks = append(weeks, wk)
	}
	sort.Strings(weeks)
	for _, wk := range weeks {
		weekly[wk].print()
	}
}
