// Price-action strategy candidate comparison — liquidity sweep (fade a wick
// that pierces a local swing level then closes back inside) vs. pin bar
// (rejection candle near the M15 20-bar S/R level, no piercing required).
// Full-history simulation against real M1 candles.
//
// v2: the first pass used the M15 20-bar rolling S/R level as the sweep
// reference — too slow-moving (~5h extreme) for a single M5 candle to pierce
// and reject in one shot; it matched 0-1 times across ~21k M5 bars, which is
// a broken test, not an answer. This version uses a local 10-bar M5 swing
// high/low (same window CalculateBreakoutLevel already uses in
// internal/indicator/structure.go) as the sweep reference instead.
//
// Also adds an H1-trend-aligned variant of both patterns: every strategy
// that actually survives live in this codebase requires the raw signal to
// agree with (or at least not fight) the H1 trend — sr_bounce, trend_follow,
// dd_ranging_breakout all do this. Testing pure candle geometry with zero
// confluence is a weak test of whether the underlying concept has an edge,
// so both patterns are reported with and without an H1-alignment filter.
//
// Both variants use the same R:R construction (SL beyond the rejection wick
// + a small ATR buffer, TP = 2x that risk) so no variant gets a friendlier
// SL/TP. Win rate is reported on RESOLVED trades only (win+loss) — the
// earlier trend_follow denominator bug taught us not to silently fold
// unresolved into the base — and alongside R-expectancy per trade, since a
// high win rate with a poor R:R can still lose money and vice versa.
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

const winMin = 1440 // 24h resolution window, same lesson as Part 8's trend_follow sim
const minATR = 1e-6
const slBufferATRFrac = 0.1
const rewardRiskRatio = 2.0
const swingLookback = 10 // matches CalculateBreakoutLevel's 10-bar window

type Candle struct {
	BarTime         time.Time
	Open, High, Low float64
}

type M5Row struct {
	BarTime                time.Time
	Open, High, Low, Close float64
}

type M15Row struct {
	BarTime                  time.Time
	Support, Resistance, ATR float64
}

type H1Row struct {
	BarTime time.Time
	Regime  string
}

type trade struct {
	entryTime     time.Time
	entry, sl, tp float64
	isBuy         bool
}

func simulate(sc []Candle, start, endTime time.Time, entry, tp, sl float64, isBuy bool) (int, time.Time) {
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
					return 1, c.BarTime
				}
				return -1, c.BarTime
			}
			if (entry - c.Low) >= (c.High - entry) {
				return 1, c.BarTime
			}
			return -1, c.BarTime
		} else if tpHit {
			return 1, c.BarTime
		} else if slHit {
			return -1, c.BarTime
		}
	}
	return 0, endTime
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

func atLatest[T any](rows []T, t time.Time, barTime func(T) time.Time) (T, bool) {
	idx := sort.Search(len(rows), func(i int) bool { return barTime(rows[i]).After(t) }) - 1
	if idx < 0 {
		var zero T
		return zero, false
	}
	return rows[idx], true
}

func recordOutcome(r *result, candles []Candle, tr trade) {
	outcome, _ := simulate(candles, tr.entryTime, tr.entryTime.Add(winMin*time.Minute), tr.entry, tr.tp, tr.sl, tr.isBuy)
	switch outcome {
	case 1:
		r.win++
	case -1:
		r.loss++
	default:
		r.unresolved++
	}
}

func runSweep(m5 []M5Row, m15 []M15Row, h1 []H1Row, candles []Candle, requireH1Align bool) result {
	var r result
	for i, bar := range m5 {
		if i < swingLookback {
			continue
		}
		ref, ok := atLatest(m15, bar.BarTime, func(x M15Row) time.Time { return x.BarTime })
		if !ok || ref.ATR < minATR {
			continue
		}
		swingHigh := m5[i-swingLookback].High
		swingLow := m5[i-swingLookback].Low
		for _, p := range m5[i-swingLookback+1 : i] {
			if p.High > swingHigh {
				swingHigh = p.High
			}
			if p.Low < swingLow {
				swingLow = p.Low
			}
		}

		var tr trade
		matched := false
		if bar.High > swingHigh && bar.Close < swingHigh {
			// bearish sweep: pierced the local swing high, closed back below — fade short
			if requireH1Align {
				if h1r, ok := atLatest(h1, bar.BarTime, func(x H1Row) time.Time { return x.BarTime }); !ok || h1r.Regime == "trending_up" {
					continue
				}
			}
			sl := bar.High + slBufferATRFrac*ref.ATR
			entry := bar.Close
			risk := sl - entry
			if risk <= 0 {
				continue
			}
			tr = trade{bar.BarTime, entry, sl, entry - rewardRiskRatio*risk, false}
			matched = true
		} else if bar.Low < swingLow && bar.Close > swingLow {
			// bullish sweep: pierced the local swing low, closed back above — fade long
			if requireH1Align {
				if h1r, ok := atLatest(h1, bar.BarTime, func(x H1Row) time.Time { return x.BarTime }); !ok || h1r.Regime == "trending_down" {
					continue
				}
			}
			sl := bar.Low - slBufferATRFrac*ref.ATR
			entry := bar.Close
			risk := entry - sl
			if risk <= 0 {
				continue
			}
			tr = trade{bar.BarTime, entry, sl, entry + rewardRiskRatio*risk, true}
			matched = true
		}
		if !matched {
			continue
		}
		r.matched++
		recordOutcome(&r, candles, tr)
	}
	return r
}

func runPinBar(m5 []M5Row, m15 []M15Row, h1 []H1Row, candles []Candle, requireH1Align bool) result {
	var r result
	for _, bar := range m5 {
		ref, ok := atLatest(m15, bar.BarTime, func(x M15Row) time.Time { return x.BarTime })
		if !ok || ref.ATR < minATR || ref.Support <= 0 || ref.Resistance <= 0 {
			continue
		}
		body := bar.Close - bar.Open
		absBody := body
		if absBody < 0 {
			absBody = -absBody
		}
		upperWick := bar.High - max(bar.Open, bar.Close)
		lowerWick := min(bar.Open, bar.Close) - bar.Low
		nearLevel := 0.3 * ref.ATR

		var tr trade
		matched := false
		if lowerWick >= 2*absBody && upperWick <= absBody && (bar.Low-ref.Support) <= nearLevel && (bar.Low-ref.Support) >= -nearLevel {
			if requireH1Align {
				if h1r, ok := atLatest(h1, bar.BarTime, func(x H1Row) time.Time { return x.BarTime }); !ok || h1r.Regime == "trending_down" {
					continue
				}
			}
			sl := bar.Low - slBufferATRFrac*ref.ATR
			entry := bar.Close
			risk := entry - sl
			if risk <= 0 {
				continue
			}
			tr = trade{bar.BarTime, entry, sl, entry + rewardRiskRatio*risk, true}
			matched = true
		} else if upperWick >= 2*absBody && lowerWick <= absBody && (ref.Resistance-bar.High) <= nearLevel && (ref.Resistance-bar.High) >= -nearLevel {
			if requireH1Align {
				if h1r, ok := atLatest(h1, bar.BarTime, func(x H1Row) time.Time { return x.BarTime }); !ok || h1r.Regime == "trending_up" {
					continue
				}
			}
			sl := bar.High + slBufferATRFrac*ref.ATR
			entry := bar.Close
			risk := sl - entry
			if risk <= 0 {
				continue
			}
			tr = trade{bar.BarTime, entry, sl, entry - rewardRiskRatio*risk, false}
			matched = true
		}
		if !matched {
			continue
		}
		r.matched++
		recordOutcome(&r, candles, tr)
	}
	return r
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: price_action_scan <postgres-url>")
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

		logf("%s: loading M5/M15/H1 market_states...", sym)
		var m5 []M5Row
		var m15 []M15Row
		var h1 []H1Row
		{
			rows, err := pool.Query(ctx, `
				SELECT bar_time, COALESCE(open,0), COALESCE(high,0), COALESCE(low,0), COALESCE(close,0)
				FROM market_states WHERE symbol_id=$1 AND period='M5' AND provider='ctrader' ORDER BY bar_time ASC`, symbolID)
			must(err)
			for rows.Next() {
				var r M5Row
				must(rows.Scan(&r.BarTime, &r.Open, &r.High, &r.Low, &r.Close))
				m5 = append(m5, r)
			}
			rows.Close()

			rows, err = pool.Query(ctx, `
				SELECT bar_time, COALESCE(support_level,0), COALESCE(resistance_level,0), COALESCE(atr,0)
				FROM market_states WHERE symbol_id=$1 AND period='M15' AND provider='ctrader' ORDER BY bar_time ASC`, symbolID)
			must(err)
			for rows.Next() {
				var r M15Row
				must(rows.Scan(&r.BarTime, &r.Support, &r.Resistance, &r.ATR))
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
		logf("%s: %d M1 candles, %d M5 bars, %d M15 bars, %d H1 bars", sym, len(candles), len(m5), len(m15), len(h1))

		sweepRaw := runSweep(m5, m15, h1, candles, false)
		sweepH1 := runSweep(m5, m15, h1, candles, true)
		pinRaw := runPinBar(m5, m15, h1, candles, false)
		pinH1 := runPinBar(m5, m15, h1, candles, true)

		print := func(name string, r result) {
			fmt.Printf("%-22s matched=%-5d win=%-4d loss=%-4d unresolved=%-4d win_rate=%.1f%%  expectancy=%.2fR\n",
				name, r.matched, r.win, r.loss, r.unresolved, r.winRate(), r.expectancyR())
		}
		fmt.Printf("\n========== %s ==========\n", sym)
		print("liquidity_sweep", sweepRaw)
		print("liquidity_sweep+H1", sweepH1)
		print("pin_bar", pinRaw)
		print("pin_bar+H1", pinH1)
	}
}

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
