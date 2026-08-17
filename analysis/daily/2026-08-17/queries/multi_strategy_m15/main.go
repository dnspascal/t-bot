package main

// Checks whether the "M15 disagreement predicts loss" pattern found in
// dd_ranging_breakout generalizes to trend_follow and ema_pullback, using
// each strategy's REAL entry gate and REAL SL/TP construction (not a
// simplified proxy) replayed against the full historical M1 candle archive.
// regime.go skipped — too many entry paths to approximate quickly/honestly.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const minATR = 1e-6
const winMin = 240

type Candle struct {
	BarTime          time.Time
	Open, High, Low  float64
}

type MS struct {
	Regime              string
	EMAFast, EMASlow    float64
	RSI, ADX, ATR       float64
	Open, Close         float64
	TrendHigh, TrendLow float64
	BarTime             time.Time
}

type SigMoment struct {
	SymbolID  string
	CreatedAt time.Time
	M5ID      string
	M15ID     string
	H1ID      string
}

type bucket struct{ n, win, loss int }

func (b *bucket) winRate() float64 {
	if b.n == 0 {
		return 0
	}
	return 100 * float64(b.win) / float64(b.n)
}

func m15Bucket(r string) string {
	if r == "" {
		return "unknown"
	}
	return r
}

// m1EntryPrice finds the M1 candle open closest to (at or just after) t — a much
// tighter proxy for the live tick price at signal time than an M5 candle close.
func m1EntryPrice(sc []Candle, t time.Time) (float64, bool) {
	idx := sort.Search(len(sc), func(i int) bool { return !sc[i].BarTime.Before(t) })
	if idx < len(sc) {
		return sc[idx].Open, true
	}
	if len(sc) > 0 {
		return sc[len(sc)-1].Open, true
	}
	return 0, false
}

// simulate returns (outcome, closeTime): outcome is 1=win, -1=loss, 0=unresolved
// by endTime; closeTime is the real bar time it resolved at (or endTime if
// unresolved), for cooldown-timing purposes.
func simulate(sc []Candle, start time.Time, endTime time.Time, entry, tp, sl float64, isBuy bool) (int, time.Time) {
	startIdx := sort.Search(len(sc), func(i int) bool { return !sc[i].BarTime.Before(start) })
	if startIdx > 0 {
		startIdx--
	}
	for i := startIdx; i < len(sc); i++ {
		c := sc[i]
		if c.BarTime.After(endTime) {
			break
		}
		var tpHit, slHit bool
		if isBuy {
			tpHit = c.High >= tp
			slHit = c.Low <= sl
		} else {
			tpHit = c.Low <= tp
			slHit = c.High >= sl
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

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: multi_strategy_m15 <postgres-url>")
		os.Exit(1)
	}
	db, err := sql.Open("pgx", os.Args[1])
	must(err)
	defer db.Close()
	ctx := context.Background()

	logf("loading M1 candles...")
	candleMap := map[string][]Candle{}
	{
		rows, err := db.QueryContext(ctx, `SELECT symbol_id::text, bar_time, open, high, low FROM candles WHERE period='M1' ORDER BY symbol_id, bar_time ASC`)
		must(err)
		for rows.Next() {
			var sid string
			var c Candle
			must(rows.Scan(&sid, &c.BarTime, &c.Open, &c.High, &c.Low))
			candleMap[sid] = append(candleMap[sid], c)
		}
		rows.Close()
	}

	logf("loading market states...")
	msMap := map[string]MS{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT id::text, COALESCE(regime,''), COALESCE(ema_fast,0), COALESCE(ema_slow,0),
			       COALESCE(rsi,0), COALESCE(adx,0), COALESCE(atr,0), COALESCE(open,0), COALESCE(close,0),
			       COALESCE(trend_high,0), COALESCE(trend_low,0), bar_time
			FROM market_states WHERE period IN ('M5','M15','H1')`)
		must(err)
		for rows.Next() {
			var id string
			var m MS
			must(rows.Scan(&id, &m.Regime, &m.EMAFast, &m.EMASlow, &m.RSI, &m.ADX, &m.ATR, &m.Open, &m.Close, &m.TrendHigh, &m.TrendLow, &m.BarTime))
			msMap[id] = m
		}
		rows.Close()
	}

	logf("loading signal moments...")
	var moments []SigMoment
	seen := map[string]bool{}
	{
		rows, err := db.QueryContext(ctx, `SELECT symbol_id::text, created_at, checked_market_states FROM signals WHERE checked_market_states IS NOT NULL ORDER BY created_at ASC`)
		must(err)
		for rows.Next() {
			var symID string
			var t time.Time
			var raw []byte
			must(rows.Scan(&symID, &t, &raw))
			var parsed map[string]map[string]string
			if err := json.Unmarshal(raw, &parsed); err != nil {
				continue
			}
			m5id := parsed["M5"]["id"]
			if m5id == "" {
				continue
			}
			key := symID + "|" + m5id
			if seen[key] {
				continue
			}
			seen[key] = true
			moments = append(moments, SigMoment{symID, t, m5id, parsed["M15"]["id"], parsed["H1"]["id"]})
		}
		rows.Close()
	}
	logf("  → %d unique M5 moments total\n", len(moments))

	// ── trend_follow ─────────────────────────────────────────────────
	// Simulates the real outcome-aware cooldown (2 consecutive same-direction
	// invalidated closes -> 60min cooldown for that direction) so the matched
	// population is directly comparable to what live trading actually sees,
	// not an inflated count that includes chained re-entries the live bot
	// would have blocked. Matches internal/strategy/trend_follow/strategy.go.
	{
		const consecutiveFailsToCooldown = 2
		const cooldownDuration = 60 * time.Minute
		type cdState struct {
			failStreak    int
			cooldownUntil time.Time
		}
		cooldown := map[string]*cdState{} // key: symbolID|dir

		buckets := map[string]*bucket{}
		matched, skippedCooldown := 0, 0
		for _, mo := range moments {
			m5, ok := msMap[mo.M5ID]
			if !ok || m5.ATR < minATR {
				continue
			}
			h1 := msMap[mo.H1ID]
			if h1.Regime == "ranging" || h1.Regime == "" {
				continue
			}
			var dir string
			switch h1.Regime {
			case "trending_up":
				dir = "BUY"
			case "trending_down":
				dir = "SELL"
			default:
				continue
			}
			if dir == "BUY" && m5.EMAFast < m5.EMASlow {
				continue
			}
			if dir == "SELL" && m5.EMAFast > m5.EMASlow {
				continue
			}
			if dir == "BUY" && m5.Regime != "trending_up" {
				continue
			}
			if dir == "SELL" && m5.Regime != "trending_down" {
				continue
			}
			if h1.ADX < 20 {
				continue
			}
			m15 := msMap[mo.M15ID]
			if m15.ATR <= 0 {
				continue
			}

			cdKey := mo.SymbolID + "|" + dir
			if cd := cooldown[cdKey]; cd != nil && mo.CreatedAt.Before(cd.cooldownUntil) {
				skippedCooldown++
				continue
			}
			matched++

			entry := m5.Close
			var tp, sl float64
			if dir == "BUY" {
				sl = entry - 1.5*m15.ATR
				tp = entry + 2.5*m15.ATR
			} else {
				sl = entry + 1.5*m15.ATR
				tp = entry - 2.5*m15.ATR
			}
			endTime := mo.CreatedAt.Add(winMin * time.Minute)
			outcome, closeTime := simulate(candleMap[mo.SymbolID], mo.CreatedAt, endTime, entry, tp, sl, dir == "BUY")

			cd := cooldown[cdKey]
			if cd == nil {
				cd = &cdState{}
				cooldown[cdKey] = cd
			}
			switch outcome {
			case -1: // invalidated
				cd.failStreak++
				if cd.failStreak >= consecutiveFailsToCooldown {
					cd.cooldownUntil = closeTime.Add(cooldownDuration)
				}
			case 1: // validated
				cd.failStreak = 0
			}

			// "M15 agrees" = M15 regime matches the trade direction; "disagrees" = opposite trend label.
			agreement := "other"
			if dir == "BUY" && m15.Regime == "trending_up" {
				agreement = "M15_agrees(trending_up)"
			} else if dir == "SELL" && m15.Regime == "trending_down" {
				agreement = "M15_agrees(trending_down)"
			} else if dir == "BUY" && m15.Regime == "trending_down" {
				agreement = "M15_disagrees(trending_down)"
			} else if dir == "SELL" && m15.Regime == "trending_up" {
				agreement = "M15_disagrees(trending_up)"
			} else if m15.Regime == "ranging" {
				agreement = "M15_ranging"
			} else if m15.Regime == "breakout" {
				agreement = "M15_breakout"
			}
			if buckets[agreement] == nil {
				buckets[agreement] = &bucket{}
			}
			b := buckets[agreement]
			b.n++
			if outcome == 1 {
				b.win++
			} else if outcome == -1 {
				b.loss++
			}
		}
		fmt.Printf("\n=== trend_follow (matched=%d, skipped_by_cooldown=%d) ===\n", matched, skippedCooldown)
		printBuckets(buckets)
	}

	// ── ema_pullback ──────────────────────────────────────────────────
	{
		const emaProximityATRs = 1.0
		const slATRMult = 1.0
		const adxTrendFloor = 25.0
		buckets := map[string]*bucket{}
		matched := 0
		for _, mo := range moments {
			h1 := msMap[mo.H1ID]
			if h1.ADX < adxTrendFloor {
				continue
			}
			var dir string
			switch h1.Regime {
			case "trending_up":
				dir = "BUY"
			case "trending_down":
				dir = "SELL"
			default:
				continue
			}
			m5, ok := msMap[mo.M5ID]
			if !ok || m5.ATR <= 0 {
				continue
			}
			// Real live code uses the tick price at signal time. M5 close was too coarse a
			// proxy (systematically stale vs. where price actually sat when the condition
			// fired) — use the nearest M1 candle open instead, much tighter.
			currentPrice, ok2 := m1EntryPrice(candleMap[mo.SymbolID], mo.CreatedAt)
			if !ok2 {
				continue
			}
			emaProximity := emaProximityATRs * m5.ATR
			dist := currentPrice - h1.EMASlow
			if dist < 0 {
				dist = -dist
			}
			if dist > emaProximity {
				continue
			}
			if dir == "BUY" && currentPrice > h1.EMASlow+emaProximity {
				continue
			}
			if dir == "SELL" && currentPrice < h1.EMASlow-emaProximity {
				continue
			}
			if dir == "BUY" && m5.Close <= m5.Open {
				continue
			}
			if dir == "SELL" && m5.Close >= m5.Open {
				continue
			}
			m15 := msMap[mo.M15ID]
			if m15.ATR <= 0 {
				continue
			}

			var sl, tp float64
			if dir == "BUY" {
				sl = h1.EMASlow - slATRMult*m15.ATR
				tp = h1.TrendHigh
			} else {
				sl = h1.EMASlow + slATRMult*m15.ATR
				tp = h1.TrendLow
			}
			if dir == "BUY" && tp <= currentPrice {
				continue
			}
			if dir == "SELL" && tp >= currentPrice {
				continue
			}
			matched++

			endTime := mo.CreatedAt.Add(winMin * time.Minute)
			outcome, _ := simulate(candleMap[mo.SymbolID], mo.CreatedAt, endTime, currentPrice, tp, sl, dir == "BUY")

			agreement := "other"
			if dir == "BUY" && m15.Regime == "trending_up" {
				agreement = "M15_agrees(trending_up)"
			} else if dir == "SELL" && m15.Regime == "trending_down" {
				agreement = "M15_agrees(trending_down)"
			} else if dir == "BUY" && m15.Regime == "trending_down" {
				agreement = "M15_disagrees(trending_down)"
			} else if dir == "SELL" && m15.Regime == "trending_up" {
				agreement = "M15_disagrees(trending_up)"
			} else if m15.Regime == "ranging" {
				agreement = "M15_ranging"
			} else if m15.Regime == "breakout" {
				agreement = "M15_breakout"
			}
			if buckets[agreement] == nil {
				buckets[agreement] = &bucket{}
			}
			b := buckets[agreement]
			b.n++
			if outcome == 1 {
				b.win++
			} else if outcome == -1 {
				b.loss++
			}
		}
		fmt.Printf("\n=== ema_pullback (matched=%d) ===\n", matched)
		printBuckets(buckets)
	}
}

func printBuckets(buckets map[string]*bucket) {
	var keys []string
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b := buckets[k]
		fmt.Printf("  %-28s n=%-5d win=%-4d loss=%-4d win_rate=%.1f%%\n", k, b.n, b.win, b.loss, b.winRate())
	}
}

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
