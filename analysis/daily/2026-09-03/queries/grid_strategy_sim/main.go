package main

// Tests whether a grid strategy (symmetric ladder of buy-the-dip /
// sell-the-rally legs around a fixed reference price, each closing for one
// grid-spacing of profit) would have worked on this account's own real
// XAUUSD history -- not a synthetic/idealized market, the actual price
// action of the last ~9 weeks, including the real trending days this
// session has been looking at directly (the ~4383->4550+ run today).
//
// Static grid: levels are fixed at referencePrice +/- n*spacing, set once
// at the start of the backtest window (no re-centering -- that's a real
// design choice grid bots make differently; this tests the simplest,
// most common version). A level refills every time price returns to it
// after its previous leg closed -- that's the actual point of a grid, a
// perpetual ladder, not a one-shot entry.
//
// Two variants tested per spacing:
//   uncapped:  no limit on simultaneous open legs per side (the "textbook"
//              grid, and the version that can blow up in a sustained trend)
//   capped:    max N simultaneous open legs per side (a risk-managed
//              version) -- once at cap, a fresh level crossing is skipped
//
// Round-trip cost per leg: $0.20 (approximating this account's real
// observed spread ~$0.10-0.12 + commission ~$0.08 on a same-sized trade).
//
// Run: go run . <postgres-url>
import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const roundTripCost = 0.20

type Candle struct {
	BarTime                time.Time
	Open, High, Low, Close float64
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

type leg struct {
	entryPrice float64
	tp         float64
}

type gridResult struct {
	spacing                             float64
	capped                              bool
	cap                                 int
	closedLegs                          int
	grossProfit                         float64
	netProfit                           float64
	maxConcurrentBuy, maxConcurrentSell int
	maxUnrealizedDD                     float64 // worst mark-to-market drawdown seen at any point
	finalOpenBuy, finalOpenSell         int
	finalUnrealized                     float64
}

func runGrid(candles []Candle, spacing float64, cap int) gridResult {
	reference := candles[0].Open

	// pre-compute a wide enough band of levels either side to cover the
	// whole historical range actually traded, so we're not artificially
	// capping how far price can wander from the reference.
	minPx, maxPx := reference, reference
	for _, c := range candles {
		if c.Low < minPx {
			minPx = c.Low
		}
		if c.High > maxPx {
			maxPx = c.High
		}
	}
	var buyLevels, sellLevels []float64
	for lvl := reference - spacing; lvl >= minPx-spacing; lvl -= spacing {
		buyLevels = append(buyLevels, lvl)
	}
	for lvl := reference + spacing; lvl <= maxPx+spacing; lvl += spacing {
		sellLevels = append(sellLevels, lvl)
	}

	openBuys := map[float64]bool{} // level -> filled/open
	openSells := map[float64]bool{}

	res := gridResult{spacing: spacing, capped: cap > 0, cap: cap}

	for _, c := range candles {
		// Close legs first (a bar can both fill a new level and close an
		// existing one; closing-first is the conservative assumption --
		// doesn't let a single bar both open AND immediately close the
		// same fresh leg for a free win).
		for lvl := range openBuys {
			tp := lvl + spacing
			if c.High >= tp {
				res.closedLegs++
				res.grossProfit += spacing
				res.netProfit += spacing - roundTripCost
				delete(openBuys, lvl)
			}
		}
		for lvl := range openSells {
			tp := lvl - spacing
			if c.Low <= tp {
				res.closedLegs++
				res.grossProfit += spacing
				res.netProfit += spacing - roundTripCost
				delete(openSells, lvl)
			}
		}

		// Open new legs where price crossed a level and we're under cap.
		for _, lvl := range buyLevels {
			if openBuys[lvl] {
				continue
			}
			if cap > 0 && len(openBuys) >= cap {
				break
			}
			if c.Low <= lvl {
				openBuys[lvl] = true
			}
		}
		for _, lvl := range sellLevels {
			if openSells[lvl] {
				continue
			}
			if cap > 0 && len(openSells) >= cap {
				break
			}
			if c.High >= lvl {
				openSells[lvl] = true
			}
		}

		if len(openBuys) > res.maxConcurrentBuy {
			res.maxConcurrentBuy = len(openBuys)
		}
		if len(openSells) > res.maxConcurrentSell {
			res.maxConcurrentSell = len(openSells)
		}

		// Mark-to-market unrealized P&L at this bar's close, to track worst drawdown.
		unrealized := 0.0
		for lvl := range openBuys {
			unrealized += c.Close - lvl
		}
		for lvl := range openSells {
			unrealized += lvl - c.Close
		}
		if unrealized < res.maxUnrealizedDD {
			res.maxUnrealizedDD = unrealized
		}
	}

	res.finalOpenBuy = len(openBuys)
	res.finalOpenSell = len(openSells)
	lastClose := candles[len(candles)-1].Close
	for lvl := range openBuys {
		res.finalUnrealized += lastClose - lvl
	}
	for lvl := range openSells {
		res.finalUnrealized += lvl - lastClose
	}

	return res
}

func (r gridResult) print() {
	capLabel := "uncapped"
	if r.capped {
		capLabel = fmt.Sprintf("cap=%d/side", r.cap)
	}
	fmt.Printf("spacing=$%-5.2f %-14s closed_legs:%-6d gross:%9.2f net:%9.2f  max_concurrent(buy/sell):%d/%d  worst_mark_to_market_DD:%9.2f  still_open(buy/sell):%d/%d unrealized_now:%9.2f\n",
		r.spacing, capLabel, r.closedLegs, r.grossProfit, r.netProfit,
		r.maxConcurrentBuy, r.maxConcurrentSell, r.maxUnrealizedDD,
		r.finalOpenBuy, r.finalOpenSell, r.finalUnrealized)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: grid_strategy_sim <postgres-url>")
		os.Exit(1)
	}
	db, err := sql.Open("pgx", os.Args[1])
	must(err)
	defer db.Close()
	ctx := context.Background()

	candles := []Candle{}
	rows, err := db.QueryContext(ctx, `
		SELECT c.bar_time, c.open, c.high, c.low, c.close
		FROM candles c JOIN symbols s ON s.id = c.symbol_id
		WHERE s.symbol='XAUUSD' AND c.period='M1' ORDER BY c.bar_time ASC`)
	must(err)
	for rows.Next() {
		var c Candle
		must(rows.Scan(&c.BarTime, &c.Open, &c.High, &c.Low, &c.Close))
		candles = append(candles, c)
	}
	must(rows.Err())
	rows.Close()

	fmt.Fprintf(os.Stderr, "loaded %d M1 candles, %s to %s, reference(open of first bar)=%.2f\n\n",
		len(candles), candles[0].BarTime.Format("2006-01-02"), candles[len(candles)-1].BarTime.Format("2006-01-02"), candles[0].Open)

	spacings := []float64{2, 5, 10}
	caps := []int{0, 3, 5, 10} // 0 = uncapped

	for _, sp := range spacings {
		for _, cap := range caps {
			r := runGrid(candles, sp, cap)
			r.print()
		}
		fmt.Println()
	}
}
