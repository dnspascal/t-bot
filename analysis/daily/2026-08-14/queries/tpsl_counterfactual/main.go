// Part 3/4 of analysis/daily/2026-08-14/report.html.
//
// For every trade closed early (reversal signals or peak-drawback), replays the
// real M5 candle high/low from close time out to open_timestamp+4h to check
// whether price would have touched the original TP or SL first. peak_drawback
// buckets are split by which minPeakGain-gate era (33%/50%/70%) the trade's
// close_timestamp falls in — see report Part 4 for why that split matters.
//
// Run: DATABASE_URL=postgres://...@localhost:5433/tbot_prod go run early_close_tpsl_counterfactual.go
// (against a local SSH tunnel to the prod DB — see report footer for the tunnel command)
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type trade struct {
	id          string
	symbolID    string
	symbol      string
	side        string
	openPrice   float64
	slPrice     float64
	tpPrice     float64
	closeReason string
	openAt      time.Time
	closeAt     time.Time
}

type candle struct {
	barTime time.Time
	high    float64
	low     float64
}

var (
	regime50Start, _ = time.Parse(time.RFC3339, "2026-08-03T13:22:00+03:00")
	regime70Start, _ = time.Parse(time.RFC3339, "2026-08-11T16:52:00+03:00")
)

func peakDrawbackRegime(closeAt time.Time) string {
	switch {
	case closeAt.Before(regime50Start):
		return "peak_drawback_gate33"
	case closeAt.Before(regime70Start):
		return "peak_drawback_gate50"
	default:
		return "peak_drawback_gate70"
	}
}

func bucketOf(reason string, closeAt time.Time) string {
	switch {
	case strings.HasPrefix(reason, "peak_drawback="):
		return peakDrawbackRegime(closeAt)
	case strings.Contains(reason, "_against"):
		n := strings.Count(reason, ",") + 1
		return fmt.Sprintf("reversal_%dsig", n)
	default:
		return ""
	}
}

func main() {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `
		SELECT p.id, p.symbol_id, sym.symbol, p.side,
		       COALESCE(p.open_price,0), COALESCE(p.current_sl,0), COALESCE(p.current_tp,0),
		       COALESCE(f.close_reason,''), p.open_timestamp, COALESCE(p.close_timestamp, now())
		FROM positions p
		JOIN symbols sym ON sym.id = p.symbol_id
		JOIN fills f ON f.our_position_id = p.id AND f.close_reason IS NOT NULL
		WHERE p.provider = 'ctrader'
		ORDER BY p.open_timestamp ASC
	`)
	if err != nil {
		panic(err)
	}
	var trades []trade
	for rows.Next() {
		var t trade
		if err := rows.Scan(&t.id, &t.symbolID, &t.symbol, &t.side, &t.openPrice, &t.slPrice, &t.tpPrice, &t.closeReason, &t.openAt, &t.closeAt); err != nil {
			panic(err)
		}
		trades = append(trades, t)
	}
	rows.Close()

	now := time.Now()
	nairobi, _ := time.LoadLocation("Africa/Nairobi")
	nowLocal := now.In(nairobi)
	todayStart := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, nairobi)
	weekday := int(nowLocal.Weekday())
	if weekday == 0 {
		weekday = 7
	} // Monday=1..Sunday=7
	weekStart := todayStart.AddDate(0, 0, -(weekday - 1))

	type outcome struct {
		hitTP, hitSL, undetermined int
	}
	// windowName -> bucket -> outcome
	results := map[string]map[string]*outcome{
		"today":     {},
		"this_week": {},
		"all_time":  {},
	}
	getOutcome := func(win, bucket string) *outcome {
		m := results[win]
		if m[bucket] == nil {
			m[bucket] = &outcome{}
		}
		return m[bucket]
	}

	candleCache := map[string][]candle{} // symbolID -> all M5 candles (loaded lazily per symbol, once)

	loadCandles := func(symbolID string) []candle {
		if c, ok := candleCache[symbolID]; ok {
			return c
		}
		rows, err := pool.Query(ctx, `
			SELECT bar_time, high, low FROM candles
			WHERE symbol_id = $1 AND period = 'M5'
			ORDER BY bar_time ASC
		`, symbolID)
		if err != nil {
			panic(err)
		}
		var cs []candle
		for rows.Next() {
			var c candle
			rows.Scan(&c.barTime, &c.high, &c.low)
			cs = append(cs, c)
		}
		rows.Close()
		candleCache[symbolID] = cs
		return cs
	}

	processed := 0
	for _, t := range trades {
		bucket := bucketOf(t.closeReason, t.closeAt)
		if bucket == "" {
			continue
		}
		if t.slPrice <= 0 || t.tpPrice <= 0 {
			continue
		}
		processed++

		horizon := t.openAt.Add(4 * time.Hour)
		cs := loadCandles(t.symbolID)
		// binary search for first candle at/after closeAt
		idx := sort.Search(len(cs), func(i int) bool { return !cs[i].barTime.Before(t.closeAt) })

		outcomeStr := "undetermined"
		for i := idx; i < len(cs) && cs[i].barTime.Before(horizon); i++ {
			c := cs[i]
			var tpHit, slHit bool
			if t.side == "BUY" {
				tpHit = c.high >= t.tpPrice
				slHit = c.low <= t.slPrice
			} else {
				tpHit = c.low <= t.tpPrice
				slHit = c.high >= t.slPrice
			}
			if tpHit && slHit {
				outcomeStr = "ambiguous_same_bar"
				break
			} else if tpHit {
				outcomeStr = "hit_tp"
				break
			} else if slHit {
				outcomeStr = "hit_sl"
				break
			}
		}

		windows := []string{"all_time"}
		if !t.openAt.Before(weekStart) {
			windows = append(windows, "this_week")
		}
		if !t.openAt.Before(todayStart) {
			windows = append(windows, "today")
		}
		for _, w := range windows {
			o := getOutcome(w, bucket)
			switch outcomeStr {
			case "hit_tp":
				o.hitTP++
			case "hit_sl":
				o.hitSL++
			default:
				o.undetermined++
			}
		}
	}

	fmt.Println("early-closed trades analyzed:", processed)
	fmt.Println()
	for _, win := range []string{"today", "this_week", "all_time"} {
		fmt.Println("=====", win, "=====")
		buckets := results[win]
		var names []string
		for b := range buckets {
			names = append(names, b)
		}
		sort.Strings(names)
		for _, b := range names {
			o := buckets[b]
			total := o.hitTP + o.hitSL + o.undetermined
			fmt.Printf("  %-16s total=%-3d would_hit_TP=%-3d (%.0f%%)  would_hit_SL=%-3d (%.0f%%)  undetermined=%-3d (%.0f%%)\n",
				b, total, o.hitTP, pct(o.hitTP, total), o.hitSL, pct(o.hitSL, total), o.undetermined, pct(o.undetermined, total))
		}
		fmt.Println()
	}
}

func pct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}
