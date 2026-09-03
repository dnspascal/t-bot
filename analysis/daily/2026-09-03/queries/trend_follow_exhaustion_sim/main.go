package main

// Real trade today: trend_follow BUY at 16:10, hit SL 34m later, -$19.86.
// At entry: M5 RSI 76.1, M15 RSI 79.0 (both overbought), H4 EMA already
// crossed bearish, D1 regime explicitly trending_down -- four independent
// signals pointing against the trade, none of which trend_follow reads (it
// checks only M5/H1 regime, M5 EMA alignment, and ADX).
//
// This replays trend_follow's REAL current entry gate exactly (as shipped
// today: H1 breakout regime handled via M5 EMA direction, M5-regime-streak
// freshness machine, ADX floor) chronologically over full XAUUSD history,
// collects every qualifying entry with its real outcome (real SL/TP math,
// real M1 price replay) AND its M5/M15 RSI + H4/D1 EMA-direction at that
// exact bar, then tests two candidate exclusion filters IN ISOLATION so a
// bad one can't hide inside a good one:
//
//   RSI-extreme:      hold if M5 or M15 RSI is past a threshold against
//                      the trade direction (tested across a grid)
//   HTF-opposition:    hold if H4 or D1's own EMA direction opposes the
//                      trade direction
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
	RSI              float64
}
type M15State struct {
	BarTime time.Time
	ATR     float64
	RSI     float64
}
type HTFState struct {
	BarTime          time.Time
	Regime           string
	EMAFast, EMASlow float64
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

type entry struct {
	barTime       time.Time
	dir           string
	outcome       int // 1 win, -1 loss, 0 unresolved
	pnl           float64
	m5RSI, m15RSI float64
	h4Dir, d1Dir  string // "up", "down", "" (unavailable)
}

func loadStates(ctx context.Context, db *sql.DB, period string, withRSI, withADX bool) (h1s []H1State) {
	q := `SELECT ms.bar_time, COALESCE(ms.regime,''), COALESCE(ms.ema_fast,0), COALESCE(ms.ema_slow,0)`
	if withADX {
		q += `, COALESCE(ms.adx,0)`
	} else {
		q += `, 0`
	}
	q += ` FROM market_states ms JOIN symbols s ON s.id = ms.symbol_id
		WHERE s.symbol='XAUUSD' AND ms.period=$1 ORDER BY ms.bar_time ASC`
	rows, err := db.QueryContext(ctx, q, period)
	must(err)
	defer rows.Close()
	for rows.Next() {
		var st H1State
		must(rows.Scan(&st.BarTime, &st.Regime, &st.EMAFast, &st.EMASlow, &st.ADX))
		h1s = append(h1s, st)
	}
	return
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: trend_follow_exhaustion_sim <postgres-url>")
		os.Exit(1)
	}
	db, err := sql.Open("pgx", os.Args[1])
	must(err)
	defer db.Close()
	ctx := context.Background()

	h1 := loadStates(ctx, db, "H1", false, true)
	h4raw := loadStates(ctx, db, "H4", false, false)
	d1raw := loadStates(ctx, db, "D1", false, false)

	m5 := []M5State{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT ms.bar_time, COALESCE(ms.regime,''), COALESCE(ms.ema_fast,0), COALESCE(ms.ema_slow,0), COALESCE(ms.close,0), COALESCE(ms.rsi,0)
			FROM market_states ms JOIN symbols s ON s.id = ms.symbol_id
			WHERE s.symbol='XAUUSD' AND ms.period='M5' ORDER BY ms.bar_time ASC`)
		must(err)
		for rows.Next() {
			var st M5State
			must(rows.Scan(&st.BarTime, &st.Regime, &st.EMAFast, &st.EMASlow, &st.Close, &st.RSI))
			m5 = append(m5, st)
		}
		rows.Close()
	}

	m15 := []M15State{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT ms.bar_time, COALESCE(ms.atr,0), COALESCE(ms.rsi,0)
			FROM market_states ms JOIN symbols s ON s.id = ms.symbol_id
			WHERE s.symbol='XAUUSD' AND ms.period='M15' ORDER BY ms.bar_time ASC`)
		must(err)
		for rows.Next() {
			var st M15State
			must(rows.Scan(&st.BarTime, &st.ATR, &st.RSI))
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
	fmt.Fprintf(os.Stderr, "loaded %d H1, %d M5, %d M15, %d H4, %d D1 states, %d M1 candles\n",
		len(h1), len(m5), len(m15), len(h4raw), len(d1raw), len(candles))

	h1Times := make([]time.Time, len(h1))
	for i, h := range h1 {
		h1Times[i] = h.BarTime
	}
	m15Times := make([]time.Time, len(m15))
	for i, m := range m15 {
		m15Times[i] = m.BarTime
	}
	h4Times := make([]time.Time, len(h4raw))
	for i, h := range h4raw {
		h4Times[i] = h.BarTime
	}
	d1Times := make([]time.Time, len(d1raw))
	for i, d := range d1raw {
		d1Times[i] = d.BarTime
	}
	nearest := func(bt time.Time, times []time.Time) int {
		i := sort.Search(len(times), func(i int) bool { return times[i].After(bt) })
		return i - 1
	}
	candleIdxFor := func(bt time.Time) int {
		return sort.Search(len(candles), func(i int) bool { return !candles[i].BarTime.Before(bt) })
	}

	simulate := func(startIdx int, start time.Time, entryPx, sl, tp float64, isBuy bool) int {
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

	var entries []entry

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

		var dir string
		switch h.Regime {
		case "trending_up":
			dir = "BUY"
		case "trending_down":
			dir = "SELL"
		case "breakout":
			if bar.EMAFast > bar.EMASlow {
				dir = "BUY"
			} else {
				dir = "SELL"
			}
		default:
			continue
		}

		isBuy := dir == "BUY"
		if isBuy && bar.EMAFast < bar.EMASlow {
			continue
		}
		if !isBuy && bar.EMAFast > bar.EMASlow {
			continue
		}
		if isBuy && bar.Regime != "trending_up" {
			continue
		}
		if !isBuy && bar.Regime != "trending_down" {
			continue
		}
		if unseenBarsTracked < minTrackedBarsBeforeTrustng {
			continue
		}
		if m5RegimeStreak > maxFreshM5RegimeStreak {
			continue
		}
		if h.ADX < adxFloor {
			continue
		}

		entryPx := bar.Close
		var sl, tp float64
		if isBuy {
			sl, tp = entryPx-slATRMult*atr, entryPx+tpATRMult*atr
		} else {
			sl, tp = entryPx+slATRMult*atr, entryPx-tpATRMult*atr
		}
		idx := candleIdxFor(bar.BarTime)
		outcome := simulate(idx, bar.BarTime, entryPx, sl, tp, isBuy)
		exitPrice := entryPx
		if outcome == 1 {
			exitPrice = tp
		} else if outcome == -1 {
			exitPrice = sl
		}
		signedExit := exitPrice
		if !isBuy {
			signedExit = 2*entryPx - exitPrice
		}
		pnl := (signedExit - entryPx) * dollarPerPt

		e := entry{barTime: bar.BarTime, dir: dir, outcome: outcome, pnl: pnl}
		e.m5RSI = bar.RSI
		e.m15RSI = m15[mi].RSI

		if h4i := nearest(bar.BarTime, h4Times); h4i >= 0 && bar.BarTime.Sub(h4raw[h4i].BarTime) <= 5*time.Hour {
			if h4raw[h4i].EMAFast > h4raw[h4i].EMASlow {
				e.h4Dir = "up"
			} else {
				e.h4Dir = "down"
			}
		}
		if d1i := nearest(bar.BarTime, d1Times); d1i >= 0 && bar.BarTime.Sub(d1raw[d1i].BarTime) <= 30*time.Hour {
			if d1raw[d1i].EMAFast > d1raw[d1i].EMASlow {
				e.d1Dir = "up"
			} else {
				e.d1Dir = "down"
			}
		}

		entries = append(entries, e)
	}

	fmt.Fprintf(os.Stderr, "qualifying entries: %d\n\n", len(entries))

	type bucket struct {
		n, win, lose, unresolved int
		pnl                      float64
	}
	summarize := func(label string, es []entry) {
		var b bucket
		for _, e := range es {
			b.n++
			switch {
			case e.outcome == 1:
				b.win++
			case e.outcome == -1:
				b.lose++
			default:
				b.unresolved++
			}
			b.pnl += e.pnl
		}
		wr := 0.0
		if b.win+b.lose > 0 {
			wr = 100 * float64(b.win) / float64(b.win+b.lose)
		}
		fmt.Printf("%-32s n:%-5d win:%-4d lose:%-4d unresolved:%-4d winrate:%5.1f%%  pnl:%9.2f\n",
			label, b.n, b.win, b.lose, b.unresolved, wr, b.pnl)
	}

	fmt.Println("=== Baseline (current live logic, no new filters) ===")
	summarize("all qualifying entries", entries)

	fmt.Println("\n=== Candidate 1: RSI-extreme filter (exclude if M5 or M15 RSI past threshold against trade dir) ===")
	for _, thr := range []float64{65, 70, 75, 80} {
		var kept, excluded []entry
		for _, e := range entries {
			exhausted := false
			if e.dir == "BUY" {
				exhausted = e.m5RSI >= thr || e.m15RSI >= thr
			} else {
				exhausted = e.m5RSI <= 100-thr || e.m15RSI <= 100-thr
			}
			if exhausted {
				excluded = append(excluded, e)
			} else {
				kept = append(kept, e)
			}
		}
		fmt.Printf("-- threshold %.0f --\n", thr)
		summarize("  kept", kept)
		summarize("  excluded (would-be-blocked)", excluded)
	}

	fmt.Println("\n=== Candidate 2: HTF-opposition filter (exclude if H4 or D1 EMA opposes trade dir) ===")
	{
		var kept, excluded, noData []entry
		for _, e := range entries {
			if e.h4Dir == "" && e.d1Dir == "" {
				noData = append(noData, e)
				kept = append(kept, e) // no data -> can't apply the filter, matches production's ok-gated skip
				continue
			}
			opposes := (e.dir == "BUY" && (e.h4Dir == "down" || e.d1Dir == "down")) ||
				(e.dir == "SELL" && (e.h4Dir == "up" || e.d1Dir == "up"))
			if opposes {
				excluded = append(excluded, e)
			} else {
				kept = append(kept, e)
			}
		}
		summarize("kept (incl. no-HTF-data)", kept)
		summarize("excluded (would-be-blocked)", excluded)
		fmt.Printf("  (%d of %d entries had no H4/D1 data within staleness bound)\n", len(noData), len(entries))
	}

	fmt.Println("\n=== Candidate 2b: D1 only ===")
	{
		var kept, excluded []entry
		for _, e := range entries {
			if e.d1Dir == "" {
				kept = append(kept, e)
				continue
			}
			opposes := (e.dir == "BUY" && e.d1Dir == "down") || (e.dir == "SELL" && e.d1Dir == "up")
			if opposes {
				excluded = append(excluded, e)
			} else {
				kept = append(kept, e)
			}
		}
		summarize("kept", kept)
		summarize("excluded (would-be-blocked)", excluded)

		fmt.Println("  -- excluded (D1-opposed) trades by week, checking it's not one bad stretch --")
		weekly := map[string][]entry{}
		for _, e := range excluded {
			wk := e.barTime.Truncate(7 * 24 * time.Hour).Format("2006-01-02")
			weekly[wk] = append(weekly[wk], e)
		}
		weeks := make([]string, 0, len(weekly))
		for wk := range weekly {
			weeks = append(weeks, wk)
		}
		sort.Strings(weeks)
		for _, wk := range weeks {
			summarize("    week of "+wk, weekly[wk])
		}
	}

	fmt.Println("\n=== Candidate 2c: H4 only ===")
	{
		var kept, excluded []entry
		for _, e := range entries {
			if e.h4Dir == "" {
				kept = append(kept, e)
				continue
			}
			opposes := (e.dir == "BUY" && e.h4Dir == "down") || (e.dir == "SELL" && e.h4Dir == "up")
			if opposes {
				excluded = append(excluded, e)
			} else {
				kept = append(kept, e)
			}
		}
		summarize("kept", kept)
		summarize("excluded (would-be-blocked)", excluded)
	}
}
