// Part 7 of analysis/daily/2026-08-14/report.html.
//
// Checks how often a trade reaches a real favorable peak (>=15 pips, measured
// on M5 candle CLOSE — matching how the live bot's own UpdatePeaks/watcher
// actually sample price, not wick highs/lows) and then sits underwater for
// >=60 minutes before it finally closes. Excludes tp_hit/breakeven_sl/
// eod_close since those are clean, expected outcomes, not the "went
// profitable then just bled for hours" pattern being checked for.
//
// Run: DATABASE_URL=postgres://...@localhost:5433/tbot_prod go run stagnation_pattern.go
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
	closeReason string
	openAt      time.Time
	closeAt     time.Time
	netProfit   float64
	pipSize     float64
}

type candle struct {
	barTime time.Time
	close   float64
}

const peakThresholdPips = 15.0
const stagnantMinutes = 60.0

func main() {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `
		SELECT p.id, p.symbol_id, p.side, COALESCE(p.open_price,0),
		       COALESCE(f.close_reason,''), p.open_timestamp, COALESCE(p.close_timestamp, now()),
		       COALESCE(f.net_profit,0), COALESCE(sc.pip_size,0)
		FROM positions p
		JOIN fills f ON f.our_position_id = p.id AND f.close_reason IS NOT NULL
		LEFT JOIN symbol_configs sc ON sc.symbol_id = p.symbol_id AND sc.deleted_at IS NULL
		WHERE p.provider = 'ctrader'
		  AND f.close_reason NOT IN ('tp_hit', 'breakeven_sl', 'eod_close')
		ORDER BY p.open_timestamp ASC
	`)
	if err != nil {
		panic(err)
	}
	var trades []trade
	for rows.Next() {
		var t trade
		rows.Scan(&t.id, &t.symbolID, &t.side, &t.openPrice, &t.closeReason, &t.openAt, &t.closeAt, &t.netProfit, &t.pipSize)
		if t.pipSize > 0 {
			trades = append(trades, t)
		}
	}
	rows.Close()

	candleCache := map[string][]candle{}
	loadCandles := func(symbolID string) []candle {
		if c, ok := candleCache[symbolID]; ok {
			return c
		}
		rows, err := pool.Query(ctx, `SELECT bar_time, close FROM candles WHERE symbol_id=$1 AND period='M5' ORDER BY bar_time ASC`, symbolID)
		if err != nil {
			panic(err)
		}
		var out []candle
		for rows.Next() {
			var c candle
			rows.Scan(&c.barTime, &c.close)
			out = append(out, c)
		}
		rows.Close()
		candleCache[symbolID] = out
		return out
	}

	type match struct {
		t                 trade
		peakPips          float64
		peakTime          time.Time
		underwaterStart   time.Time
		underwaterMinutes float64
	}
	var matches []match
	totalConsidered := 0

	for _, t := range trades {
		candles := loadCandles(t.symbolID)
		startIdx := sort.Search(len(candles), func(i int) bool { return !candles[i].barTime.Before(t.openAt) })
		endIdx := sort.Search(len(candles), func(i int) bool { return candles[i].barTime.After(t.closeAt) })
		if endIdx-startIdx < 2 {
			continue
		}
		totalConsidered++

		peakPips, peakTime := 0.0, t.openAt
		for i := startIdx; i < endIdx; i++ {
			c := candles[i]
			var pips float64
			if t.side == "BUY" {
				pips = (c.close - t.openPrice) / t.pipSize
			} else {
				pips = (t.openPrice - c.close) / t.pipSize
			}
			if pips > peakPips {
				peakPips = pips
				peakTime = c.barTime
			}
		}
		if peakPips < peakThresholdPips {
			continue
		}

		// find the LAST transition from profitable(>0) to unprofitable(<=0) after the peak
		var underwaterStart time.Time
		wasProfitable := true
		for i := startIdx; i < endIdx; i++ {
			c := candles[i]
			if c.barTime.Before(peakTime) {
				continue
			}
			var pips float64
			if t.side == "BUY" {
				pips = (c.close - t.openPrice) / t.pipSize
			} else {
				pips = (t.openPrice - c.close) / t.pipSize
			}
			nowProfitable := pips > 0
			if wasProfitable && !nowProfitable {
				underwaterStart = c.barTime
			}
			wasProfitable = nowProfitable
		}
		if underwaterStart.IsZero() {
			continue // never actually went underwater after the peak (e.g. closed while still up)
		}
		underwaterMin := t.closeAt.Sub(underwaterStart).Minutes()
		if underwaterMin < stagnantMinutes {
			continue
		}
		matches = append(matches, match{t, peakPips, peakTime, underwaterStart, underwaterMin})
	}

	fmt.Printf("Trades considered (had candle coverage, excluded tp_hit/breakeven_sl/eod_close): %d\n", totalConsidered)
	fmt.Printf("Matching 'profitable (>=%.0f pips) then stagnant/underwater >=%.0fmin before close': %d\n\n", peakThresholdPips, stagnantMinutes, len(matches))

	var totalNet float64
	for _, m := range matches {
		totalNet += m.t.netProfit
		fmt.Printf("%s  peak=%.1fp@%s  underwater %.0fmin (from %s to close)  reason=%s  net=$%.2f\n",
			m.t.id[:8], m.peakPips, m.peakTime.Format("15:04"), m.underwaterMinutes,
			m.underwaterStart.Format("01-02 15:04"), m.t.closeReason, m.t.netProfit)
	}
	fmt.Printf("\nAggregate net P&L of matching trades: $%.2f across %d trades (avg $%.2f)\n", totalNet, len(matches), totalNet/float64(max(len(matches), 1)))
}
