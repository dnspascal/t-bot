package main

// Scoped variant of v3_condition_analysis: instead of the 38 times
// dd_ranging_breakout actually fired live, replays EVERY historical moment
// (83,977 signal-check moments across the bot's whole history) that matches
// its exact condition shape (H1=ranging, M5=breakout, EMA bull, RSI>=50),
// then buckets by finer RSI ranges to properly test whether high-RSI entries
// ("chasing") are actually worse — the live-trade-only sample (38) was too
// thin to trust. Same M1-candle touch-simulation methodology as v3.

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

const tpMult = 1.5
const slMult = 1.0 // matches live dd_ranging_breakout today
const minATR = 1e-6
const winMin = 240 // 4h, matches the strategy's own backtest convention

type Candle struct {
	BarTime time.Time
	High    float64
	Low     float64
}

type MS struct {
	Regime  string
	EMAFast float64
	EMASlow float64
	RSI     float64
	ADX     float64
	ATR     float64
	Close   float64
}

type SigMoment struct {
	SymbolID  string
	CreatedAt time.Time
	M5ID      string
	M15ID     string
	H1ID      string
}

func rsiBucket(rsi float64) string {
	switch {
	case rsi >= 90:
		return "90-100"
	case rsi >= 80:
		return "80-90"
	case rsi >= 70:
		return "70-80"
	case rsi >= 60:
		return "60-70"
	default:
		return "50-60"
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: rsi_scan <postgres-url>")
		os.Exit(1)
	}
	db, err := sql.Open("pgx", os.Args[1])
	must(err)
	defer db.Close()
	ctx := context.Background()

	logf("loading M1 candles...")
	candleMap := map[string][]Candle{}
	{
		rows, err := db.QueryContext(ctx, `SELECT symbol_id::text, bar_time, high, low FROM candles WHERE period='M1' ORDER BY symbol_id, bar_time ASC`)
		must(err)
		for rows.Next() {
			var sid string
			var c Candle
			must(rows.Scan(&sid, &c.BarTime, &c.High, &c.Low))
			candleMap[sid] = append(candleMap[sid], c)
		}
		rows.Close()
	}

	logf("loading market states...")
	msMap := map[string]MS{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT id::text, COALESCE(regime,''), COALESCE(ema_fast,0), COALESCE(ema_slow,0),
			       COALESCE(rsi,0), COALESCE(adx,0), COALESCE(atr,0), COALESCE(close,0)
			FROM market_states WHERE period IN ('M5','M15','H1')`)
		must(err)
		for rows.Next() {
			var id string
			var m MS
			must(rows.Scan(&id, &m.Regime, &m.EMAFast, &m.EMASlow, &m.RSI, &m.ADX, &m.ATR, &m.Close))
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
	logf("  → %d unique M5 moments total", len(moments))

	type bucket struct {
		n, win, loss int
	}
	buckets := map[string]*bucket{}

	matched := 0
	for _, mo := range moments {
		m5, ok := msMap[mo.M5ID]
		if !ok || m5.ATR < minATR || m5.Close == 0 {
			continue
		}
		h1 := msMap[mo.H1ID]

		// dd_ranging_breakout's exact live condition shape:
		if h1.Regime != "ranging" {
			continue
		}
		if m5.Regime != "breakout" {
			continue
		}
		if m5.EMAFast <= m5.EMASlow {
			continue
		}
		if m5.RSI < 50 {
			continue
		}
		matched++

		b := rsiBucket(m5.RSI)
		if buckets[b] == nil {
			buckets[b] = &bucket{}
		}
		buckets[b].n++

		entry := m5.Close
		tp := entry + tpMult*m5.ATR
		sl := entry - slMult*m5.ATR

		sc := candleMap[mo.SymbolID]
		startIdx := sort.Search(len(sc), func(i int) bool { return !sc[i].BarTime.Before(mo.CreatedAt) })
		if startIdx > 0 {
			startIdx--
		}
		endTime := mo.CreatedAt.Add(winMin * time.Minute)

		for i := startIdx; i < len(sc); i++ {
			c := sc[i]
			if c.BarTime.After(endTime) {
				break
			}
			tpHit := c.High >= tp
			slHit := c.Low <= sl
			if tpHit && slHit {
				if (c.High - entry) >= (entry - c.Low) {
					buckets[b].win++
				} else {
					buckets[b].loss++
				}
				break
			} else if tpHit {
				buckets[b].win++
				break
			} else if slHit {
				buckets[b].loss++
				break
			}
		}
	}

	logf("  → %d moments matched dd_ranging_breakout's condition shape", matched)
	fmt.Println()
	fmt.Println("RSI bucket | N | Win | Loss | Win rate | (unresolved by 4h = N - Win - Loss)")
	order := []string{"50-60", "60-70", "70-80", "80-90", "90-100"}
	for _, k := range order {
		b := buckets[k]
		if b == nil || b.n == 0 {
			fmt.Printf("%-10s | 0 | - | - | -\n", k)
			continue
		}
		fmt.Printf("%-10s | %d | %d | %d | %.1f%% | unresolved=%d\n", k, b.n, b.win, b.loss, 100*float64(b.win)/float64(b.n), b.n-b.win-b.loss)
	}
}

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
