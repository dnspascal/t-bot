package main

// Two live trend_follow BUYs today entered right at the top of a fast,
// recent spike (price ran ~$31-33 in 20-30 minutes, entry landed within
// minutes of the peak), not mid-trend. Two prior candidates (RSI-extreme
// level, H4/D1 static direction -- see trend_follow_exhaustion_sim) were
// both rejected by backtest. Neither actually encodes "chasing a fast
// recent move" -- they're level signals, not velocity signals. This tests
// that specific hypothesis: how far and how fast did price move in the N
// M5 bars immediately before entry, in the trade's direction, normalized
// by M15 ATR (so it's comparable across calm/volatile periods) -- and does
// excluding large-recent-velocity entries improve trend_follow's results.
//
// Replays the exact current entry gate (same as trend_follow_exhaustion_sim)
// chronologically, real SL/TP math, real M1 price replay.
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

type entry struct {
	barTime     time.Time
	dir         string
	outcome     int
	pnl         float64
	velocityATR map[int]float64 // lookback bars -> velocity in ATR units
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: trend_follow_spike_chase_sim <postgres-url>")
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
		must(rows.Err())
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
		must(rows.Err())
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
		must(rows.Err())
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
		must(rows.Err())
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

	lookbacks := []int{3, 6, 9} // M5 bars: 15min, 30min, 45min

	var entries []entry

	var lastM5Regime string
	var lastM5BarTime int64
	m5RegimeStreak := 0
	unseenBarsTracked := 0

	for i, bar := range m5 {
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

		e := entry{barTime: bar.BarTime, dir: dir, outcome: outcome, pnl: pnl, velocityATR: map[int]float64{}}
		for _, lb := range lookbacks {
			if i-lb < 0 || atr <= 0 {
				continue
			}
			priorClose := m5[i-lb].Close
			move := entryPx - priorClose
			if !isBuy {
				move = priorClose - entryPx
			}
			e.velocityATR[lb] = move / atr
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
			switch e.outcome {
			case 1:
				b.win++
			case -1:
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
		fmt.Printf("%-40s n:%-5d win:%-4d lose:%-4d unresolved:%-4d winrate:%5.1f%%  pnl:%9.2f\n",
			label, b.n, b.win, b.lose, b.unresolved, wr, b.pnl)
	}

	fmt.Println("=== Baseline ===")
	summarize("all qualifying entries", entries)

	bestLabel := ""
	var bestExcluded []entry
	bestDelta := 0.0

	for _, lb := range lookbacks {
		fmt.Printf("\n=== Velocity over trailing %d M5 bars (%dmin), in ATR units ===\n", lb, lb*5)
		for _, thr := range []float64{1.0, 1.5, 2.0, 2.5, 3.0} {
			var kept, excluded []entry
			for _, e := range entries {
				v, ok := e.velocityATR[lb]
				if !ok {
					kept = append(kept, e)
					continue
				}
				if v >= thr {
					excluded = append(excluded, e)
				} else {
					kept = append(kept, e)
				}
			}
			label := fmt.Sprintf("  thr=%.1fx ATR", thr)
			summarize(label+" kept", kept)
			summarize(label+" excluded", excluded)

			var keptPnl, exclPnl float64
			for _, e := range kept {
				keptPnl += e.pnl
			}
			for _, e := range excluded {
				exclPnl += e.pnl
			}
			delta := keptPnl - exclPnl // how much better kept avg is vs excluded, weighted by improvement if we cut excluded
			if len(excluded) >= 20 && exclPnl < 0 && delta > bestDelta {
				bestDelta = delta
				bestLabel = fmt.Sprintf("lookback=%d thr=%.1f", lb, thr)
				bestExcluded = excluded
			}
		}
	}

	if bestLabel != "" {
		fmt.Printf("\n=== Best candidate: %s -- excluded trades by week ===\n", bestLabel)
		weekly := map[string][]entry{}
		for _, e := range bestExcluded {
			wk := e.barTime.Truncate(7 * 24 * time.Hour).Format("2006-01-02")
			weekly[wk] = append(weekly[wk], e)
		}
		weeks := make([]string, 0, len(weekly))
		for wk := range weekly {
			weeks = append(weeks, wk)
		}
		sort.Strings(weeks)
		for _, wk := range weeks {
			summarize("  week of "+wk, weekly[wk])
		}
	} else {
		fmt.Println("\nNo candidate found where excluded bucket was net negative with n>=20.")
	}
}
