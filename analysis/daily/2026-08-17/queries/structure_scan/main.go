// Market-structure strategy candidate test — break of structure (BOS,
// trend-continuation) vs. change of character (CHoCH, trend-reversal).
// Neither concept exists anywhere in this codebase: TrendHigh/TrendLow are
// just rolling 20-bar max/min (internal/indicator/structure.go), not real
// confirmed swing pivots. This tool builds a real fractal swing-pivot
// detector from scratch and tests both trading applications of it, using
// the same full-history M1 simulation and 2:1 R:R construction as
// price_action_scan/main.go so the results are directly comparable.
//
// Pivot detection: 5-bar fractal on M15 (N=2 each side) — bar i is a
// confirmed swing high once bar i+2 closes and bar[i].High is the max of
// bar[i-2..i+2]. Confirmation lag (2 M15 bars = 30min) is real: the pivot
// is only usable starting at its confirmedAt time, no lookahead.
//
// Structure state: uptrend once the last two confirmed swing highs are a
// higher-high (HH) AND the last two confirmed swing lows are a higher-low
// (HL). Downtrend is the LH/LL mirror. Otherwise undefined (transitional —
// no signal).
//
// BOS-continuation: while structure is uptrend, close breaks above the
// active (unconsumed) confirmed swing high -> BUY (trend continuation).
// Downtrend mirror for SELL.
//
// CHoCH-reversal: while structure is downtrend (LH/LL), close breaks above
// the active confirmed swing high -> BUY (this violates the down-structure,
// i.e. a reversal signal). Uptrend mirror for SELL.
//
// Both use M15 close for entry (structure is inherently a slower-timeframe
// concept, unlike the M5-candle-geometry patterns in price_action_scan).
// SL beyond the broken level + ATR buffer, TP = 2x that risk. Each is also
// tested with an H1-trend-alignment filter, same as price_action_scan,
// for direct comparability.
//
// Run: DATABASE_URL=postgres://...@localhost:5433/tbot_prod go run main.go
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const winMin = 1440
const minATR = 1e-6
const slBufferATRFrac = 0.1
const rewardRiskRatio = 2.0
const fractalN = 2 // 5-bar fractal: N bars each side

type Candle struct {
	BarTime         time.Time
	Open, High, Low float64
}

type M15Bar struct {
	BarTime                time.Time
	Open, High, Low, Close float64
	ATR                    float64
}

type H1Row struct {
	BarTime time.Time
	Regime  string
}

type pivot struct {
	price       float64
	confirmedAt time.Time
}

type signal struct {
	kind          string // "bos" or "choch"
	isBuy         bool
	entryTime     time.Time
	entry, sl, tp float64
	h1Regime      string
}

func atLatest[T any](rows []T, t time.Time, barTime func(T) time.Time) (T, bool) {
	idx := sort.Search(len(rows), func(i int) bool { return barTime(rows[i]).After(t) }) - 1
	if idx < 0 {
		var zero T
		return zero, false
	}
	return rows[idx], true
}

func simulate(sc []Candle, start, endTime time.Time, entry, tp, sl float64, isBuy bool) int {
	startIdx := sort.Search(len(sc), func(i int) bool { return !sc[i].BarTime.Before(start) })
	for i := startIdx; i < len(sc); i++ {
		c := sc[i]
		if c.BarTime.After(endTime) {
			break
		}
		var tpHit, slHit bool
		if isBuy {
			tpHit, slHit = c.High >= tp, c.Low <= sl
		} else {
			tpHit, slHit = c.Low <= tp, c.High >= sl
		}
		if tpHit && slHit {
			if isBuy {
				if (c.High - entry) >= (entry - c.Low) {
					return 1
				}
				return -1
			}
			if (entry - c.Low) >= (c.High - entry) {
				return 1
			}
			return -1
		} else if tpHit {
			return 1
		} else if slHit {
			return -1
		}
	}
	return 0
}

// detectSignals walks the M15 series once, maintaining confirmed swing
// pivots and structure state, and emits every BOS/CHoCH break event.
func detectSignals(m15 []M15Bar) []signal {
	var signals []signal

	// Build confirmed swing highs/lows first (needs full lookahead within
	// the fractal window only, which is legitimate — confirmedAt already
	// encodes when this pivot becomes knowable).
	type pivotEvt struct {
		isHigh      bool
		price       float64
		confirmedAt time.Time
	}
	var events []pivotEvt
	for i := fractalN; i < len(m15)-fractalN; i++ {
		isHigh := true
		isLow := true
		for k := 1; k <= fractalN; k++ {
			if m15[i-k].High > m15[i].High || m15[i+k].High > m15[i].High {
				isHigh = false
			}
			if m15[i-k].Low < m15[i].Low || m15[i+k].Low < m15[i].Low {
				isLow = false
			}
		}
		if isHigh {
			events = append(events, pivotEvt{true, m15[i].High, m15[i+fractalN].BarTime})
		}
		if isLow {
			events = append(events, pivotEvt{false, m15[i].Low, m15[i+fractalN].BarTime})
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].confirmedAt.Before(events[j].confirmedAt) })

	var prevSH, lastSH, prevSL, lastSL *pivot
	shActive, slActive := true, true // whether the active level hasn't been broken yet
	structure := "undefined"

	evIdx := 0
	for _, bar := range m15 {
		// apply any pivot confirmations that have landed by this bar's time
		for evIdx < len(events) && !events[evIdx].confirmedAt.After(bar.BarTime) {
			ev := events[evIdx]
			evIdx++
			if ev.isHigh {
				prevSH, lastSH = lastSH, &pivot{ev.price, ev.confirmedAt}
				shActive = true
			} else {
				prevSL, lastSL = lastSL, &pivot{ev.price, ev.confirmedAt}
				slActive = true
			}
			// reclassify structure whenever we get a fresh pivot pair
			if prevSH != nil && prevSL != nil && lastSH != nil && lastSL != nil {
				hh := lastSH.price > prevSH.price
				hl := lastSL.price > prevSL.price
				lh := lastSH.price < prevSH.price
				ll := lastSL.price < prevSL.price
				switch {
				case hh && hl:
					structure = "up"
				case lh && ll:
					structure = "down"
				default:
					structure = "undefined"
				}
			}
		}

		if bar.ATR < minATR {
			continue
		}

		if shActive && lastSH != nil && bar.Close > lastSH.price {
			shActive = false
			kind := ""
			switch structure {
			case "up":
				kind = "bos"
			case "down":
				kind = "choch"
			}
			if kind != "" && lastSL != nil {
				// Long trade breaking above a swing high: SL belongs below
				// the structure's own opposing boundary — the most recent
				// confirmed swing low — not an arbitrary ATR distance.
				entry := bar.Close
				sl := lastSL.price - slBufferATRFrac*bar.ATR
				riskAmt := entry - sl
				if riskAmt > 0 {
					signals = append(signals, signal{kind, true, bar.BarTime, entry, sl, entry + rewardRiskRatio*riskAmt, ""})
				}
			}
		}
		if slActive && lastSL != nil && bar.Close < lastSL.price {
			slActive = false
			kind := ""
			switch structure {
			case "down":
				kind = "bos"
			case "up":
				kind = "choch"
			}
			if kind != "" && lastSH != nil {
				// Short trade breaking below a swing low: SL belongs above
				// the structure's own opposing boundary — the most recent
				// confirmed swing high.
				entry := bar.Close
				sl := lastSH.price + slBufferATRFrac*bar.ATR
				riskAmt := sl - entry
				if riskAmt > 0 {
					signals = append(signals, signal{kind, false, bar.BarTime, entry, sl, entry - rewardRiskRatio*riskAmt, ""})
				}
			}
		}
	}
	return signals
}

type result struct {
	matched, win, loss, unresolved int
}

func (r result) winRate() float64 {
	total := r.win + r.loss
	if total == 0 {
		return 0
	}
	return 100 * float64(r.win) / float64(total)
}

func (r result) expectancyR() float64 {
	total := r.win + r.loss
	if total == 0 {
		return 0
	}
	return (rewardRiskRatio*float64(r.win) - float64(r.loss)) / float64(total)
}

func aggregate(signals []signal, kind string, requireH1Align bool, h1 []H1Row, candles []Candle) result {
	var r result
	for _, s := range signals {
		if s.kind != kind {
			continue
		}
		if requireH1Align {
			h1r, ok := atLatest(h1, s.entryTime, func(x H1Row) time.Time { return x.BarTime })
			if !ok {
				continue
			}
			if s.isBuy && h1r.Regime == "trending_down" {
				continue
			}
			if !s.isBuy && h1r.Regime == "trending_up" {
				continue
			}
		}
		r.matched++
		outcome := simulate(candles, s.entryTime, s.entryTime.Add(winMin*time.Minute), s.entry, s.tp, s.sl, s.isBuy)
		switch outcome {
		case 1:
			r.win++
		case -1:
			r.loss++
		default:
			r.unresolved++
		}
	}
	return r
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: structure_scan <postgres-url>")
		os.Exit(1)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Args[1])
	must(err)
	defer pool.Close()

	for _, sym := range []string{"XAUUSD", "EURUSD"} {
		var symbolID string
		must(pool.QueryRow(ctx, `SELECT id FROM symbols WHERE symbol=$1`, sym).Scan(&symbolID))

		logf("%s: loading M1 candles...", sym)
		var candles []Candle
		{
			rows, err := pool.Query(ctx, `SELECT bar_time, open, high, low FROM candles WHERE period='M1' AND symbol_id=$1 ORDER BY bar_time ASC`, symbolID)
			must(err)
			for rows.Next() {
				var c Candle
				must(rows.Scan(&c.BarTime, &c.Open, &c.High, &c.Low))
				candles = append(candles, c)
			}
			rows.Close()
		}

		logf("%s: loading M15/H1 market_states...", sym)
		var m15 []M15Bar
		var h1 []H1Row
		{
			rows, err := pool.Query(ctx, `
				SELECT bar_time, COALESCE(open,0), COALESCE(high,0), COALESCE(low,0), COALESCE(close,0), COALESCE(atr,0)
				FROM market_states WHERE symbol_id=$1 AND period='M15' AND provider='ctrader' ORDER BY bar_time ASC`, symbolID)
			must(err)
			for rows.Next() {
				var r M15Bar
				must(rows.Scan(&r.BarTime, &r.Open, &r.High, &r.Low, &r.Close, &r.ATR))
				m15 = append(m15, r)
			}
			rows.Close()

			rows, err = pool.Query(ctx, `
				SELECT bar_time, COALESCE(regime,'')
				FROM market_states WHERE symbol_id=$1 AND period='H1' AND provider='ctrader' ORDER BY bar_time ASC`, symbolID)
			must(err)
			for rows.Next() {
				var r H1Row
				must(rows.Scan(&r.BarTime, &r.Regime))
				h1 = append(h1, r)
			}
			rows.Close()
		}
		logf("%s: %d M1 candles, %d M15 bars, %d H1 bars", sym, len(candles), len(m15), len(h1))

		signals := detectSignals(m15)
		var bosCount, chochCount int
		for _, s := range signals {
			if s.kind == "bos" {
				bosCount++
			} else {
				chochCount++
			}
		}
		logf("%s: %d BOS signals, %d CHoCH signals detected", sym, bosCount, chochCount)

		bosRaw := aggregate(signals, "bos", false, h1, candles)
		bosH1 := aggregate(signals, "bos", true, h1, candles)
		chochRaw := aggregate(signals, "choch", false, h1, candles)
		chochH1 := aggregate(signals, "choch", true, h1, candles)

		print := func(name string, r result) {
			fmt.Printf("%-18s matched=%-5d win=%-4d loss=%-4d unresolved=%-4d win_rate=%.1f%%  expectancy=%.2fR\n",
				name, r.matched, r.win, r.loss, r.unresolved, r.winRate(), r.expectancyR())
		}
		fmt.Printf("\n========== %s ==========\n", sym)
		print("bos_continuation", bosRaw)
		print("bos+H1", bosH1)
		print("choch_reversal", chochRaw)
		print("choch+H1", chochH1)
	}
}

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
