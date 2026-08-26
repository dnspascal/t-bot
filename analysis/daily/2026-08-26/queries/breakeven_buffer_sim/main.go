package main

// Tests replacing the fixed breakeven_buffer_pips=2 with an ATR-scaled buffer
// (mult * M5 ATR at the moment breakeven triggers), against the 19 real
// breakeven_sl closes in history. For each candidate multiplier, replays
// from the real trigger moment (position_adjustments.reason='break_even')
// using real M1 candles to see which level is hit first: TP, or the wider
// candidate stop. Reports, per trade, whether the outcome flips from
// "cut short" to "reached TP", and whether trades that genuinely needed
// protection would now take a bigger real loss with the wider stop.
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
	BarTime         time.Time
	Open, High, Low float64
}

type Trade struct {
	PosProviderID string
	Symbol        string
	Side          string
	OpenPrice     float64
	TP            float64
	ActualNewSL   float64
	TriggerTime   time.Time
	ActualNetPnL  float64
	SymbolID      string
	ATR           float64
	Volume        float64
}

// Stable $-per-price-unit-per-1-volume-unit, derived from real sl_hit trades
// (large, well-defined moves) rather than each breakeven trade's own tiny,
// noise-dominated move — avoids divide-by-near-zero blowups.
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

// simulate returns (outcome, hitTime): 1=TP hit first, -1=SL hit first, 0=unresolved within window.
func simulate(sc []Candle, start, endTime time.Time, tp, sl float64, isBuy bool) (int, time.Time) {
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
			// same-candle ambiguity: assume worse case (SL) to stay conservative
			return -1, c.BarTime
		} else if tpHit {
			return 1, c.BarTime
		} else if slHit {
			return -1, c.BarTime
		}
	}
	return 0, endTime
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: breakeven_buffer_sim <postgres-url>")
		os.Exit(1)
	}
	db, err := sql.Open("pgx", os.Args[1])
	must(err)
	defer db.Close()
	ctx := context.Background()

	trades := []Trade{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT p.provider_position_id, s.symbol, p.side, p.open_price, p.current_tp,
			       pa.new_sl, pa.adjusted_at, f.net_profit, p.symbol_id::text, p.volume
			FROM position_adjustments pa
			JOIN positions p ON p.id = pa.position_id
			JOIN symbols s ON s.id = p.symbol_id
			JOIN fills f ON f.our_position_id = p.id AND f.event_type='close' AND f.close_reason='breakeven_sl'
			WHERE pa.reason = 'break_even'`)
		must(err)
		for rows.Next() {
			var t Trade
			must(rows.Scan(&t.PosProviderID, &t.Symbol, &t.Side, &t.OpenPrice, &t.TP, &t.ActualNewSL, &t.TriggerTime, &t.ActualNetPnL, &t.SymbolID, &t.Volume))
			trades = append(trades, t)
		}
		rows.Close()
	}
	fmt.Fprintf(os.Stderr, "loaded %d breakeven trigger events\n", len(trades))

	// ATR at trigger time (M5, nearest prior state).
	for i := range trades {
		row := db.QueryRowContext(ctx, `
			SELECT COALESCE(ms.atr,0) FROM market_states ms
			WHERE ms.symbol_id = $1::uuid AND ms.period='M5' AND ms.bar_time <= $2
			ORDER BY ms.bar_time DESC LIMIT 1`, trades[i].SymbolID, trades[i].TriggerTime)
		var atr float64
		if err := row.Scan(&atr); err == nil {
			trades[i].ATR = atr
		}
	}

	// Real M1 candles per symbol.
	candles := map[string][]Candle{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT symbol_id::text, bar_time, open, high, low
			FROM candles WHERE period='M1' ORDER BY symbol_id, bar_time ASC`)
		must(err)
		for rows.Next() {
			var sid string
			var c Candle
			must(rows.Scan(&sid, &c.BarTime, &c.Open, &c.High, &c.Low))
			candles[sid] = append(candles[sid], c)
		}
		rows.Close()
	}

	multipliers := []float64{0.3, 0.4, 0.5, 0.6}

	fmt.Printf("\n%-14s %-6s %-4s %10s %10s", "position", "sym", "side", "actual_pnl", "ATR")
	for _, m := range multipliers {
		fmt.Printf(" | mult=%.1f", m)
	}
	fmt.Println()

	// Track aggregate flips per multiplier.
	type agg struct {
		flippedToWin, stillProtected, biggerLoss, unresolved int
		newTotalPnL, actualTotalPnL                          float64
	}
	aggs := map[float64]*agg{}
	for _, m := range multipliers {
		aggs[m] = &agg{}
	}

	for _, t := range trades {
		if t.ATR <= 0 {
			continue
		}
		isBuy := t.Side == "BUY"
		dollarPerUnit := dollarPerUnitPerVolume[t.Symbol] * t.Volume

		fmt.Printf("%-14s %-6s %-4s %10.2f %10.4f", t.PosProviderID, t.Symbol, t.Side, t.ActualNetPnL, t.ATR)
		sc := candles[t.SymbolID]

		for _, m := range multipliers {
			var sl float64
			buffer := m * t.ATR
			if isBuy {
				sl = t.OpenPrice - buffer // wider stop below entry (BUY) — allows more room than a plain breakeven
			} else {
				sl = t.OpenPrice + buffer
			}
			outcome, _ := simulate(sc, t.TriggerTime, t.TriggerTime.Add(24*time.Hour), t.TP, sl, isBuy)
			a := aggs[m]
			a.actualTotalPnL += t.ActualNetPnL
			switch outcome {
			case 1:
				tpMove := t.TP - t.OpenPrice
				if !isBuy {
					tpMove = t.OpenPrice - t.TP
				}
				estPnL := tpMove * dollarPerUnit
				fmt.Printf(" | WIN $%6.2f", estPnL)
				a.flippedToWin++
				a.newTotalPnL += estPnL
			case -1:
				move := sl - t.OpenPrice
				if !isBuy {
					move = t.OpenPrice - sl
				}
				estPnL := move * dollarPerUnit
				fmt.Printf(" | SL  $%6.2f", estPnL)
				a.newTotalPnL += estPnL
				if estPnL < -0.5 {
					a.biggerLoss++
				} else {
					a.stillProtected++
				}
			default:
				fmt.Printf(" | unresolved")
				a.unresolved++
				a.newTotalPnL += t.ActualNetPnL // treat unresolved as a wash vs actual
			}
		}
		fmt.Println()
	}

	fmt.Println("\n=== summary across all trades, per multiplier ===")
	fmt.Printf("%-8s %10s %18s %18s %18s %12s %14s %14s\n", "mult", "actual_$", "flipped_to_TP_win", "still_~breakeven", "now_bigger_loss", "unresolved", "new_total_$", "delta_$")
	for _, m := range multipliers {
		a := aggs[m]
		fmt.Printf("%-8.1f %10.2f %18d %18d %18d %12d %14.2f %14.2f\n", m, a.actualTotalPnL, a.flippedToWin, a.stillProtected, a.biggerLoss, a.unresolved, a.newTotalPnL, a.newTotalPnL-a.actualTotalPnL)
	}
}
