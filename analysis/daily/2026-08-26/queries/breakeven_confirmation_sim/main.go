package main

// Tests replacing the instant resting-stop trigger for breakeven with a
// confirmation requirement: instead of amending the broker SL (which fires
// on the first tick touch), keep the SL where it is and only send an active
// close once an M1 candle actually CLOSES past the breakeven level — filters
// single-tick noise while keeping the exit price itself unchanged (still a
// fee-sized amount, never a real loss). Real cost being measured: between
// checks there's no resting stop, so a genuine reversal may travel further
// before a confirmed close triggers the exit.
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
	BarTime                 time.Time
	Open, High, Low, Close  float64
}

type Trade struct {
	PosProviderID string
	Symbol        string
	Side          string
	OpenPrice     float64
	TP            float64
	ActualNewSL   float64 // the true breakeven level (fee-sized), unchanged
	TriggerTime   time.Time
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

// simulate walks M1 candles from start. TP still triggers on a wick touch
// (unchanged — that's a real broker TP order, not being modified here).
// The breakeven level only triggers once an M1 candle's CLOSE has passed it.
func simulate(sc []Candle, start, endTime time.Time, tp, beLevel float64, isBuy bool) (outcome int, exitPrice float64, hitTime time.Time) {
	startIdx := sort.Search(len(sc), func(i int) bool { return !sc[i].BarTime.Before(start) })
	for i := startIdx; i < len(sc); i++ {
		c := sc[i]
		if c.BarTime.After(endTime) {
			break
		}
		var tpHit bool
		if isBuy {
			tpHit = c.High >= tp
		} else {
			tpHit = c.Low <= tp
		}
		if tpHit {
			return 1, tp, c.BarTime
		}
		var beConfirmed bool
		if isBuy {
			beConfirmed = c.Close <= beLevel
		} else {
			beConfirmed = c.Close >= beLevel
		}
		if beConfirmed {
			return -1, c.Close, c.BarTime
		}
	}
	return 0, 0, endTime
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: breakeven_confirmation_sim <postgres-url>")
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

	var actualTotal, newTotal float64
	flippedToWin, stillProtected, worseButStillSmall, unresolved := 0, 0, 0, 0

	fmt.Printf("%-14s %-6s %-4s %10s %8s | %s\n", "position", "sym", "side", "actual_$", "outcome", "new_$ (confirmation-gated)")
	for _, t := range trades {
		isBuy := t.Side == "BUY"
		dollarPerUnit := dollarPerUnitPerVolume[t.Symbol] * t.Volume
		sc := candles[t.SymbolID]

		outcome, exitPrice, _ := simulate(sc, t.TriggerTime, t.TriggerTime.Add(24*time.Hour), t.TP, t.ActualNewSL, isBuy)
		actualTotal += t.ActualNetPnL

		switch outcome {
		case 1:
			tpMove := t.TP - t.OpenPrice
			if !isBuy {
				tpMove = t.OpenPrice - t.TP
			}
			estPnL := tpMove * dollarPerUnit
			fmt.Printf("%-14s %-6s %-4s %10.2f %8s | WIN $%.2f\n", t.PosProviderID, t.Symbol, t.Side, t.ActualNetPnL, "TP", estPnL)
			newTotal += estPnL
			flippedToWin++
		case -1:
			move := exitPrice - t.OpenPrice
			if !isBuy {
				move = t.OpenPrice - exitPrice
			}
			estPnL := move * dollarPerUnit
			fmt.Printf("%-14s %-6s %-4s %10.2f %8s | BE(confirmed) $%.2f\n", t.PosProviderID, t.Symbol, t.Side, t.ActualNetPnL, "BE", estPnL)
			newTotal += estPnL
			if estPnL < -1.0 {
				worseButStillSmall++ // flag if the confirmation gap let it run meaningfully worse
			} else {
				stillProtected++
			}
		default:
			fmt.Printf("%-14s %-6s %-4s %10.2f %8s | unresolved\n", t.PosProviderID, t.Symbol, t.Side, t.ActualNetPnL, "?")
			newTotal += t.ActualNetPnL
			unresolved++
		}
	}

	fmt.Printf("\n=== summary ===\n")
	fmt.Printf("actual total (real, instant-touch trigger): $%.2f\n", actualTotal)
	fmt.Printf("new total (confirmation-gated trigger):      $%.2f\n", newTotal)
	fmt.Printf("delta: $%.2f\n", newTotal-actualTotal)
	fmt.Printf("flipped to real TP win: %d | still small/fee-sized on BE: %d | BE exit meaningfully worse (>$1): %d | unresolved: %d\n",
		flippedToWin, stillProtected, worseButStillSmall, unresolved)
}
