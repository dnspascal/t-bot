// Part 6 of analysis/daily/2026-08-14/report.html.
//
// Replays the real per-bar market_states (regime/rsi/ema_fast/ema_slow/
// momentum_direction — the exact fields countReversalSignals in watcher.go
// uses live) for every trade closed via reversal signals, to test whether
// requiring the "3+ signals against" condition to hold for N consecutive M5
// bars (instead of firing on the first bar, today's behavior) would help.
// Validates itself first: N=1 replay should reproduce the real close time.
//
// Run: DATABASE_URL=postgres://...@localhost:5433/tbot_prod go run confirmation_window_sim.go
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type trade struct {
	id          string
	symbolID    string
	side        string
	openPrice   float64
	slPrice     float64
	tpPrice     float64
	closeReason string
	openAt      time.Time
	closeAt     time.Time
}

type msRow struct {
	barTime  time.Time
	regime   string
	rsi      float64
	emaFast  float64
	emaSlow  float64
	momentum string
}

type candle struct {
	barTime          time.Time
	high, low, close float64
}

const rsiMidline = 50.0

func reversalCount(side string, m msRow) int {
	n := 0
	if (side == "BUY" && (m.regime == "trending_down" || m.regime == "ranging")) ||
		(side == "SELL" && (m.regime == "trending_up" || m.regime == "ranging")) {
		n++
	}
	if (side == "BUY" && m.rsi < rsiMidline) || (side == "SELL" && m.rsi > rsiMidline) {
		n++
	}
	if (side == "BUY" && m.emaFast < m.emaSlow) || (side == "SELL" && m.emaFast > m.emaSlow) {
		n++
	}
	if (side == "BUY" && m.momentum == "falling") || (side == "SELL" && m.momentum == "rising") {
		n++
	}
	return n
}

type simResult struct {
	outcome  string // "reversal_close" | "sl_hit" | "tp_hit" | "undetermined"
	closeBar time.Time
	price    float64
}

func main() {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `
		SELECT p.id, p.symbol_id, p.side,
		       COALESCE(p.open_price,0), COALESCE(p.current_sl,0), COALESCE(p.current_tp,0),
		       COALESCE(f.close_reason,''), p.open_timestamp, COALESCE(p.close_timestamp, now())
		FROM positions p
		JOIN fills f ON f.our_position_id = p.id AND f.close_reason IS NOT NULL
		WHERE p.provider = 'ctrader' AND f.close_reason LIKE '%_against%'
		ORDER BY p.open_timestamp ASC
	`)
	if err != nil {
		panic(err)
	}
	var trades []trade
	for rows.Next() {
		var t trade
		rows.Scan(&t.id, &t.symbolID, &t.side, &t.openPrice, &t.slPrice, &t.tpPrice, &t.closeReason, &t.openAt, &t.closeAt)
		trades = append(trades, t)
	}
	rows.Close()

	msCache := map[string][]msRow{}
	loadMS := func(symbolID string) []msRow {
		if c, ok := msCache[symbolID]; ok {
			return c
		}
		rows, err := pool.Query(ctx, `
			SELECT bar_time, COALESCE(regime,''), COALESCE(rsi,50), COALESCE(ema_fast,0), COALESCE(ema_slow,0), COALESCE(momentum_direction,'')
			FROM market_states WHERE symbol_id=$1 AND period='M5' AND provider='ctrader'
			ORDER BY bar_time ASC
		`, symbolID)
		if err != nil {
			panic(err)
		}
		var out []msRow
		for rows.Next() {
			var m msRow
			rows.Scan(&m.barTime, &m.regime, &m.rsi, &m.emaFast, &m.emaSlow, &m.momentum)
			out = append(out, m)
		}
		rows.Close()
		msCache[symbolID] = out
		return out
	}

	candleCache := map[string][]candle{}
	loadCandles := func(symbolID string) []candle {
		if c, ok := candleCache[symbolID]; ok {
			return c
		}
		rows, err := pool.Query(ctx, `SELECT bar_time, high, low, close FROM candles WHERE symbol_id=$1 AND period='M5' ORDER BY bar_time ASC`, symbolID)
		if err != nil {
			panic(err)
		}
		var out []candle
		for rows.Next() {
			var c candle
			rows.Scan(&c.barTime, &c.high, &c.low, &c.close)
			out = append(out, c)
		}
		rows.Close()
		candleCache[symbolID] = out
		return out
	}

	simulate := func(t trade, confirmN int) simResult {
		mstates := loadMS(t.symbolID)
		candles := loadCandles(t.symbolID)
		horizon := t.openAt.Add(4 * time.Hour)

		msIdx := sort.Search(len(mstates), func(i int) bool { return mstates[i].barTime.After(t.openAt) })

		consecutive := 0
		for mi := msIdx; mi < len(mstates) && mstates[mi].barTime.Before(horizon); mi++ {
			m := mstates[mi]

			ci := sort.Search(len(candles), func(i int) bool { return !candles[i].barTime.Before(m.barTime) })
			haveCandle := ci < len(candles) && candles[ci].barTime.Equal(m.barTime)
			if haveCandle {
				c := candles[ci]
				var tpHit, slHit bool
				if t.side == "BUY" {
					tpHit = c.high >= t.tpPrice
					slHit = c.low <= t.slPrice
				} else {
					tpHit = c.low <= t.tpPrice
					slHit = c.high >= t.slPrice
				}
				if slHit {
					return simResult{"sl_hit", m.barTime, t.slPrice}
				}
				if tpHit {
					return simResult{"tp_hit", m.barTime, t.tpPrice}
				}
			}

			n := reversalCount(t.side, m)
			if n >= 3 {
				consecutive++
			} else {
				consecutive = 0
			}
			if consecutive >= confirmN {
				var exitPrice float64
				if haveCandle {
					exitPrice = candles[ci].close
				}
				return simResult{"reversal_close", m.barTime, exitPrice}
			}
		}
		return simResult{"undetermined", time.Time{}, 0}
	}

	pipsResult := func(t trade, r simResult) float64 {
		if r.price == 0 {
			return 0
		}
		if t.side == "BUY" {
			return r.price - t.openPrice
		}
		return t.openPrice - r.price
	}

	// Validate N=1 against reality.
	matchN1, total := 0, 0
	results := map[int][]struct {
		trade trade
		res   simResult
	}{1: {}, 2: {}, 3: {}}

	for _, t := range trades {
		if t.slPrice <= 0 || t.tpPrice <= 0 {
			continue
		}
		total++
		for _, n := range []int{1, 2, 3} {
			r := simulate(t, n)
			results[n] = append(results[n], struct {
				trade trade
				res   simResult
			}{t, r})
			if n == 1 && r.outcome == "reversal_close" {
				diff := r.closeBar.Sub(t.closeAt)
				if diff < 0 {
					diff = -diff
				}
				if diff <= 10*time.Minute {
					matchN1++
				}
			}
		}
	}
	fmt.Printf("Validation: N=1 simulated close matches actual close (within 10min) for %d/%d trades\n\n", matchN1, total)

	// Build lookup of N=1 outcome per trade id for transition comparison.
	n1ByID := map[string]simResult{}
	for _, r := range results[1] {
		n1ByID[r.trade.id] = r.res
	}

	for _, n := range []int{1, 2, 3} {
		outcomes := map[string]int{}
		for _, r := range results[n] {
			outcomes[r.res.outcome]++
		}
		fmt.Printf("=== confirmN=%d === outcomes: %v\n", n, outcomes)

		if n > 1 {
			flipToTP, flipToSL, stillReversalLater, stillUndetermined := 0, 0, 0, 0
			var tpDeltaSum, slDeltaSum, laterDeltaSum float64
			for _, r := range results[n] {
				base := n1ByID[r.trade.id]
				if base.outcome != "reversal_close" {
					continue // only look at trades that were reversal-closed under current live behavior
				}
				delta := pipsResult(r.trade, r.res) - pipsResult(r.trade, base) // +pips = better than N=1, -pips = worse
				switch r.res.outcome {
				case "tp_hit":
					flipToTP++
					tpDeltaSum += delta
				case "sl_hit":
					flipToSL++
					slDeltaSum += delta
				case "reversal_close":
					stillReversalLater++
					laterDeltaSum += delta
				case "undetermined":
					stillUndetermined++
				}
			}
			avg := func(sum float64, count int) float64 {
				if count == 0 {
					return 0
				}
				return sum / float64(count)
			}
			fmt.Printf("  of trades reversal-closed under today's rule (N=1):\n")
			fmt.Printf("    now ride to TP  = %d  (avg %+.1f pips vs N=1 — positive means confirmation helped)\n", flipToTP, avg(tpDeltaSum, flipToTP))
			fmt.Printf("    now ride to SL  = %d  (avg %+.1f pips vs N=1 — negative means confirmation cost us)\n", flipToSL, avg(slDeltaSum, flipToSL))
			fmt.Printf("    still reversal-closed, just later = %d  (avg %+.1f pips vs N=1)\n", stillReversalLater, avg(laterDeltaSum, stillReversalLater))
			fmt.Printf("    undetermined at 4h = %d\n", stillUndetermined)
			eligible := flipToTP + flipToSL + stillReversalLater + stillUndetermined
			fmt.Printf("    NET pips across %d N=1-reversal-closed trades vs N=1 baseline: %+.1f\n", eligible, tpDeltaSum+slDeltaSum+laterDeltaSum)
		}
		fmt.Println()
	}
}
