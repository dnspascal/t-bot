package main

// The `breakout` strategy's H1 gate only allows a BUY when H1.Regime ==
// trending_up, SELL when trending_down — "breakout" (a real, distinct,
// usually-directional H1 regime, same one trend_follow was just fixed to
// trade) gets treated as unconfirmed. This replays the strategy's own real
// state machine — pending-breakout confirmation, lastBreakoutBarTime dedup,
// M15 ADX gates — chronologically over full XAUUSD history, comparing:
//
//   current:      today's actual gate (breakout regime -> always blocked)
//   with_breakout: breakout regime allowed too, direction taken from H1's
//                  own EMA fast vs slow (fast>slow -> up, else down) —
//                  must AGREE with the pending M15 breakout's own direction
//
// using the strategy's real SL/TP math (SL=1.0x M15 ATR, TP=2.0x M15 ATR)
// replayed against real M1 price data. Cooldown-after-repeated-failure is
// NOT replicated (depends on ClassifyCloseReason's multi-indicator "against"
// checks, not modeled here) — same limitation as trend_follow's sim.
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
	slATRMult    = 1.0
	tpATRMult    = 2.0
	adxTooHigh   = 35.0
	simWindowHrs = 24
	dollarPerPt  = 1.0
)

type H1State struct {
	BarTime          time.Time
	Regime           string
	EMAFast, EMASlow float64
}
type M15State struct {
	BarTime       time.Time
	Close         float64
	BreakoutLevel float64
	ADX           float64
	ATR           float64
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
		fmt.Fprintln(os.Stderr, "usage: breakout_h1_regime_sim <postgres-url>")
		os.Exit(1)
	}
	db, err := sql.Open("pgx", os.Args[1])
	must(err)
	defer db.Close()
	ctx := context.Background()

	h1 := []H1State{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT ms.bar_time, COALESCE(ms.regime,''), COALESCE(ms.ema_fast,0), COALESCE(ms.ema_slow,0)
			FROM market_states ms JOIN symbols s ON s.id = ms.symbol_id
			WHERE s.symbol='XAUUSD' AND ms.period='H1' ORDER BY ms.bar_time ASC`)
		must(err)
		for rows.Next() {
			var st H1State
			must(rows.Scan(&st.BarTime, &st.Regime, &st.EMAFast, &st.EMASlow))
			h1 = append(h1, st)
		}
		rows.Close()
	}

	m15 := []M15State{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT ms.bar_time, COALESCE(ms.close,0), COALESCE(ms.breakout_level,0), COALESCE(ms.adx,0), COALESCE(ms.atr,0)
			FROM market_states ms JOIN symbols s ON s.id = ms.symbol_id
			WHERE s.symbol='XAUUSD' AND ms.period='M15' ORDER BY ms.bar_time ASC`)
		must(err)
		for rows.Next() {
			var st M15State
			must(rows.Scan(&st.BarTime, &st.Close, &st.BreakoutLevel, &st.ADX, &st.ATR))
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
	fmt.Fprintf(os.Stderr, "loaded %d H1, %d M15 states, %d M1 candles\n", len(h1), len(m15), len(candles))

	h1Times := make([]time.Time, len(h1))
	for i, h := range h1 {
		h1Times[i] = h.BarTime
	}
	nearest := func(bt time.Time, times []time.Time) int {
		i := sort.Search(len(times), func(i int) bool { return times[i].After(bt) })
		return i - 1
	}
	candleIdxFor := func(bt time.Time) int {
		return sort.Search(len(candles), func(i int) bool { return !candles[i].BarTime.Before(bt) })
	}

	current := &result{name: "current (baseline)"}
	added := &result{name: "ADDED by breakout regime"}
	combined := &result{name: "combined total"}
	weekly := map[string]*result{}

	var pendingDir string
	var pendingLevel float64
	var pendingBarTime time.Time
	var lastBreakoutBarTime time.Time

	tryEnter := func(dir string, level float64, bar M15State) {
		if bar.ADX > adxTooHigh {
			return
		}
		hi := nearest(bar.BarTime, h1Times)
		if hi < 0 || bar.BarTime.Sub(h1[hi].BarTime) > 65*time.Minute {
			return // no H1 gate applicable, same as production's "H1 not warmed up" skip -- excluded from both buckets to keep comparison clean
		}
		h := h1[hi]

		allowed := (dir == "BUY" && h.Regime == "trending_up") || (dir == "SELL" && h.Regime == "trending_down")
		addedAllowed := false
		if !allowed && h.Regime == "breakout" {
			h1Dir := "SELL"
			if h.EMAFast > h.EMASlow {
				h1Dir = "BUY"
			}
			addedAllowed = h1Dir == dir
		}
		if !allowed && !addedAllowed {
			return
		}

		if bar.ATR <= 0 {
			return
		}
		entry := bar.Close
		var sl, tp float64
		isBuy := dir == "BUY"
		if isBuy {
			sl, tp = entry-slATRMult*bar.ATR, entry+tpATRMult*bar.ATR
		} else {
			sl, tp = entry+slATRMult*bar.ATR, entry-tpATRMult*bar.ATR
		}
		slPips := (entry - sl)
		if slPips < 0 {
			slPips = -slPips
		}
		if slPips < 0.30 { // 3 pips at 0.10 pipSize, matches production's floor
			return
		}

		idx := candleIdxFor(bar.BarTime)
		outcome := simulate(candles, idx, bar.BarTime, entry, sl, tp, isBuy)
		exitPrice := entry
		switch outcome {
		case 1:
			exitPrice = tp
		case -1:
			exitPrice = sl
		}
		signedExit := exitPrice
		if !isBuy {
			signedExit = 2*entry - exitPrice
		}

		lastBreakoutBarTime = bar.BarTime
		if addedAllowed {
			added.record(outcome, entry, signedExit)
			wk := bar.BarTime.Truncate(7 * 24 * time.Hour).Format("2006-01-02")
			if weekly[wk] == nil {
				weekly[wk] = &result{name: "  week of " + wk}
			}
			weekly[wk].record(outcome, entry, signedExit)
		} else {
			current.record(outcome, entry, signedExit)
		}
		combined.record(outcome, entry, signedExit)
	}

	for _, bar := range m15 {
		if pendingDir != "" && !bar.BarTime.Equal(pendingBarTime) {
			dir, level := pendingDir, pendingLevel
			pendingDir = ""
			held := (dir == "BUY" && bar.Close > level) || (dir == "SELL" && bar.Close < level)
			if held {
				tryEnter(dir, level, bar)
			}
			continue
		}

		if bar.BreakoutLevel == 0 {
			continue
		}
		if bar.ADX > adxTooHigh {
			continue
		}
		if bar.BarTime.Equal(lastBreakoutBarTime) {
			continue
		}
		var dir string
		switch {
		case bar.Close > bar.BreakoutLevel:
			dir = "BUY"
		case bar.Close < bar.BreakoutLevel:
			dir = "SELL"
		default:
			continue
		}
		pendingDir = dir
		pendingLevel = bar.BreakoutLevel
		pendingBarTime = bar.BarTime
	}

	fmt.Println()
	current.print()
	added.print()
	combined.print()

	fmt.Println("\nADDED (breakout regime) trades by week — checking it's not one lucky week:")
	weeks := make([]string, 0, len(weekly))
	for wk := range weekly {
		weeks = append(weeks, wk)
	}
	sort.Strings(weeks)
	for _, wk := range weeks {
		weekly[wk].print()
	}
}
