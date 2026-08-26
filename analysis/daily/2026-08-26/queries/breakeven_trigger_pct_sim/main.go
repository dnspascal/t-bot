package main

// Tests raising breakEvenTriggerPct itself (currently 25%) — not the buffer
// size, not the trigger mechanism. For each of the 19 real breakeven
// closes, walks M1 candles from the position's OPEN time to find when price
// first reaches each candidate trigger percentage of TP progress, then
// simulates forward from THAT later moment using the same tiny fee-sized
// buffer and the same instant-touch resting-stop logic as today — only the
// timing of when protection engages changes.
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

type Candle struct {
	BarTime                time.Time
	Open, High, Low, Close float64
}

type Trade struct {
	PosProviderID string
	Symbol        string
	Side          string
	OpenPrice     float64
	OpenTime      time.Time
	TP            float64
	BufferAbs     float64 // |ActualNewSL - OpenPrice|, the true fee-sized buffer, unchanged
	ActualNetPnL  float64
	SymbolID      string
	Volume        float64
}

var dollarPerUnitPerVolume = map[string]float64{
	"EURUSD": 1127.4984 / 100000,
	"XAUUSD": 1.0183 / 100,
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

// findTrigger returns the bar_time of the first M1 candle where price has
// moved pct% of the way from open to TP (using candle close as the progress
// marker), or zero time if never reached within the window.
func findTrigger(sc []Candle, start, endTime time.Time, openPrice, tp float64, isBuy bool, pct float64) time.Time {
	target := openPrice + pct/100*(tp-openPrice)
	startIdx := sort.Search(len(sc), func(i int) bool { return !sc[i].BarTime.Before(start) })
	for i := startIdx; i < len(sc); i++ {
		c := sc[i]
		if c.BarTime.After(endTime) {
			break
		}
		if isBuy {
			if c.High >= target {
				return c.BarTime
			}
		} else {
			if c.Low <= target {
				return c.BarTime
			}
		}
	}
	return time.Time{}
}

func simulate(sc []Candle, start, endTime time.Time, tp, beLevel float64, isBuy bool) (outcome int, exitPrice float64) {
	startIdx := sort.Search(len(sc), func(i int) bool { return !sc[i].BarTime.Before(start) })
	for i := startIdx; i < len(sc); i++ {
		c := sc[i]
		if c.BarTime.After(endTime) {
			break
		}
		var tpHit, beHit bool
		if isBuy {
			tpHit, beHit = c.High >= tp, c.Low <= beLevel
		} else {
			tpHit, beHit = c.Low <= tp, c.High >= beLevel
		}
		if tpHit && beHit {
			return -1, beLevel // conservative: assume stop side wins on same-candle ambiguity
		} else if tpHit {
			return 1, tp
		} else if beHit {
			return -1, beLevel
		}
	}
	return 0, 0
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: breakeven_trigger_pct_sim <postgres-url>")
		os.Exit(1)
	}
	db, err := sql.Open("pgx", os.Args[1])
	must(err)
	defer db.Close()
	ctx := context.Background()

	trades := []Trade{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT p.provider_position_id, s.symbol, p.side, p.open_price, p.open_timestamp, p.current_tp,
			       abs(pa.new_sl - p.open_price), f.net_profit, p.symbol_id::text, p.volume
			FROM position_adjustments pa
			JOIN positions p ON p.id = pa.position_id
			JOIN symbols s ON s.id = p.symbol_id
			JOIN fills f ON f.our_position_id = p.id AND f.event_type='close' AND f.close_reason='breakeven_sl'
			WHERE pa.reason = 'break_even' AND p.open_timestamp IS NOT NULL`)
		must(err)
		for rows.Next() {
			var t Trade
			must(rows.Scan(&t.PosProviderID, &t.Symbol, &t.Side, &t.OpenPrice, &t.OpenTime, &t.TP, &t.BufferAbs, &t.ActualNetPnL, &t.SymbolID, &t.Volume))
			trades = append(trades, t)
		}
		rows.Close()
	}
	fmt.Fprintf(os.Stderr, "loaded %d trades\n", len(trades))

	candles := map[string][]Candle{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT symbol_id::text, bar_time, open, high, low, close
			FROM candles WHERE period='M1' ORDER BY symbol_id, bar_time ASC`)
		must(err)
		for rows.Next() {
			var sid string
			var c Candle
			must(rows.Scan(&sid, &c.BarTime, &c.Open, &c.High, &c.Low, &c.Close))
			candles[sid] = append(candles[sid], c)
		}
		rows.Close()
	}

	pcts := []float64{25, 30, 35, 40, 45, 50}
	type agg struct {
		total                                            float64
		flippedWin, stillBE, neverTriggered, unresolved int
	}
	aggs := map[float64]*agg{}
	for _, p := range pcts {
		aggs[p] = &agg{}
	}
	var actualTotal float64

	for _, t := range trades {
		isBuy := t.Side == "BUY"
		dollarPerUnit := dollarPerUnitPerVolume[t.Symbol] * t.Volume
		sc := candles[t.SymbolID]
		actualTotal += t.ActualNetPnL
		windowEnd := t.OpenTime.Add(48 * time.Hour)

		for _, pct := range pcts {
			trig := findTrigger(sc, t.OpenTime, windowEnd, t.OpenPrice, t.TP, isBuy, pct)
			a := aggs[pct]
			if trig.IsZero() {
				a.neverTriggered++
				a.total += t.ActualNetPnL // never reached this level — assume same as actual (rough)
				continue
			}
			var beLevel float64
			if isBuy {
				beLevel = t.OpenPrice + t.BufferAbs
			} else {
				beLevel = t.OpenPrice - t.BufferAbs
			}
			outcome, exitPrice := simulate(sc, trig, windowEnd, t.TP, beLevel, isBuy)
			switch outcome {
			case 1:
				tpMove := t.TP - t.OpenPrice
				if !isBuy {
					tpMove = t.OpenPrice - t.TP
				}
				a.total += tpMove * dollarPerUnit
				a.flippedWin++
			case -1:
				move := exitPrice - t.OpenPrice
				if !isBuy {
					move = t.OpenPrice - exitPrice
				}
				a.total += move * dollarPerUnit
				a.stillBE++
			default:
				a.total += t.ActualNetPnL
				a.unresolved++
			}
		}
	}

	fmt.Printf("\nactual total (25%% trigger, as currently live): $%.2f\n\n", actualTotal)
	fmt.Printf("%-8s %10s %12s %10s %10s %10s %16s\n", "trigger%", "total_$", "delta_$", "TP_win", "still_BE", "unresolved", "never_reached_lvl")
	for _, pct := range pcts {
		a := aggs[pct]
		fmt.Printf("%-8.0f %10.2f %12.2f %10d %10d %10d %16d\n", pct, a.total, a.total-actualTotal, a.flippedWin, a.stillBE, a.unresolved, a.neverTriggered)
	}
}
