package main

// v3 fixes two blind spots found in v2_condition_analysis_2026-08-04 (which
// produced dd_ranging_breakout / dd_oversold_bounce — both looked like 60-70%
// win rates in the backtest and then got stopped out live in 9-27 seconds):
//
//  1. v2 scanned M5-bar High/Low for TP/SL hits. A 5-minute bar is far coarser
//     than the noise a 0.5-1.0x ATR stop lives inside — it can't see the order
//     or speed of touches within a bar. v3 scans M1 candles instead.
//  2. v2's scan started at the first bar strictly AFTER the signal's own bar
//     (sc[i].BarTime.After(m.CreatedAt)), so any reversal in the signal's own
//     bar — exactly where live SL hits were happening — was invisible to the
//     backtest. v3 starts at the bar containing the signal moment itself.
//
// Also sweeps SL multiple (0.5 / 1.0 / 1.5x ATR) instead of hardcoding 0.5,
// since that's the exact parameter today's live fix changed.

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

var slMults = []float64{0.5, 1.0, 1.5}

const minATR = 1e-6

const minN = 30

var windows = []int{10, 30, 60, 120, 240}

type Candle struct {
	BarTime time.Time
	Open    float64
	High    float64
	Low     float64
}

type MS struct {
	Regime   string
	EMAFast  float64
	EMASlow  float64
	RSI      float64
	ADX      float64
	ATR      float64
	Close    float64
	Momentum string
}

type SigMoment struct {
	SymbolID  string
	CreatedAt time.Time
	M5ID      string
	M15ID     string
	H1ID      string
}

type Cond struct {
	H1Regime   string
	M15Regime  string
	M5Regime   string
	M5EMA      string
	M5RSI      string
	M5ADX      string
	M5Momentum string
}

type WinCount struct {
	N       int
	BuyWin  int
	BuyLoss int
	SelWin  int
	SelLoss int
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: v3_condition_analysis <postgres-url>")
		os.Exit(1)
	}

	db, err := sql.Open("pgx", os.Args[1])
	must(err)
	defer db.Close()

	ctx := context.Background()

	logf("loading M1 candles (fixes v2's M5-bar blind spot)...")
	candleMap := map[string][]Candle{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT symbol_id::text, bar_time, open, high, low
			FROM candles
			WHERE period = 'M1'
			ORDER BY symbol_id, bar_time ASC
		`)
		must(err)
		for rows.Next() {
			var sid string
			var c Candle
			must(rows.Scan(&sid, &c.BarTime, &c.Open, &c.High, &c.Low))
			candleMap[sid] = append(candleMap[sid], c)
		}
		must(rows.Err())
		rows.Close()
	}
	logf("  → %d symbols, candle counts:", len(candleMap))
	for sid, cs := range candleMap {
		logf("    symbol_id=%s  candles=%d", sid, len(cs))
	}

	logf("loading market states (M5, M15, H1)...")
	msMap := map[string]MS{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT
			  id::text,
			  COALESCE(regime, ''),
			  COALESCE(ema_fast, 0),
			  COALESCE(ema_slow, 0),
			  COALESCE(rsi, 0),
			  COALESCE(adx, 0),
			  COALESCE(atr, 0),
			  COALESCE(close, 0),
			  COALESCE(momentum_direction, '')
			FROM market_states
			WHERE period IN ('M5', 'M15', 'H1')
		`)
		must(err)
		for rows.Next() {
			var id string
			var ms MS
			must(rows.Scan(&id, &ms.Regime, &ms.EMAFast, &ms.EMASlow,
				&ms.RSI, &ms.ADX, &ms.ATR, &ms.Close, &ms.Momentum))
			msMap[id] = ms
		}
		must(rows.Err())
		rows.Close()
	}
	logf("  → %d market states loaded", len(msMap))

	logf("loading signals (deduplicating by symbol + M5 state)...")
	var moments []SigMoment
	seen := map[string]bool{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT symbol_id::text, created_at, checked_market_states
			FROM signals
			WHERE checked_market_states IS NOT NULL
			ORDER BY created_at ASC
		`)
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
			m5id := stateID(parsed, "M5")
			if m5id == "" {
				continue
			}

			key := symID + "|" + m5id
			if seen[key] {
				continue
			}
			seen[key] = true

			moments = append(moments, SigMoment{
				SymbolID:  symID,
				CreatedAt: t,
				M5ID:      m5id,
				M15ID:     stateID(parsed, "M15"),
				H1ID:      stateID(parsed, "H1"),
			})
		}
		must(rows.Err())
		rows.Close()
	}
	logf("  → %d unique M5 bar moments to analyse", len(moments))

	for _, slMult := range slMults {
		logf("=== SL=%.1fx ATR / TP=%.1fx ATR ===", slMult, tpMult)
		runForSLMult(moments, msMap, candleMap, slMult)
	}
}

func runForSLMult(moments []SigMoment, msMap map[string]MS, candleMap map[string][]Candle, slMult float64) {
	type GroupData = map[int]*WinCount
	results := map[Cond]GroupData{}

	skipped := 0
	for _, m := range moments {
		m5, ok := msMap[m.M5ID]
		if !ok || m5.ATR < minATR || m5.Close == 0 {
			skipped++
			continue
		}

		m15 := msMap[m.M15ID]
		h1 := msMap[m.H1ID]

		cond := Cond{
			H1Regime:   classRegime(h1.Regime),
			M15Regime:  classRegime(m15.Regime),
			M5Regime:   classRegime(m5.Regime),
			M5EMA:      classEMA(m5.EMAFast, m5.EMASlow),
			M5RSI:      classRSI(m5.RSI),
			M5ADX:      classADX(m5.ADX),
			M5Momentum: classMomentum(m5.Momentum),
		}

		if results[cond] == nil {
			results[cond] = GroupData{}
		}

		entry := m5.Close
		buyTP := entry + tpMult*m5.ATR
		buySL := entry - slMult*m5.ATR
		selTP := entry - tpMult*m5.ATR
		selSL := entry + slMult*m5.ATR

		sc := candleMap[m.SymbolID]
		// Fix: start at the bar CONTAINING the signal moment, not strictly
		// after it — v2 skipped the entry bar entirely, where live SL hits
		// were actually happening.
		startIdx := sort.Search(len(sc), func(i int) bool {
			return !sc[i].BarTime.Before(m.CreatedAt)
		})
		if startIdx > 0 {
			startIdx-- // include the M1 bar the signal moment falls inside
		}

		for _, winMin := range windows {
			if results[cond][winMin] == nil {
				results[cond][winMin] = &WinCount{}
			}
			wc := results[cond][winMin]
			wc.N++

			endTime := m.CreatedAt.Add(time.Duration(winMin) * time.Minute)

			buyDone := false
			selDone := false

			for i := startIdx; i < len(sc); i++ {
				c := sc[i]
				if c.BarTime.After(endTime) {
					break
				}

				if !buyDone {
					tpHit := c.High >= buyTP
					slHit := c.Low <= buySL
					if tpHit && slHit {
						if (c.High - entry) >= (entry - c.Low) {
							wc.BuyWin++
						} else {
							wc.BuyLoss++
						}
						buyDone = true
					} else if tpHit {
						wc.BuyWin++
						buyDone = true
					} else if slHit {
						wc.BuyLoss++
						buyDone = true
					}
				}

				if !selDone {
					tpHit := c.Low <= selTP
					slHit := c.High >= selSL
					if tpHit && slHit {
						if (entry - c.Low) >= (c.High - entry) {
							wc.SelWin++
						} else {
							wc.SelLoss++
						}
						selDone = true
					} else if tpHit {
						wc.SelWin++
						selDone = true
					} else if slHit {
						wc.SelLoss++
						selDone = true
					}
				}

				if buyDone && selDone {
					break
				}
			}
		}
	}

	logf("  done. skipped=%d (no M5 state or ATR=0)", skipped)

	type OutRow struct {
		cond  Cond
		data  GroupData
		maxWR float64
	}

	var outRows []OutRow
	for cond, data := range results {
		wc4h := data[240]
		if wc4h == nil || wc4h.N < minN {
			continue
		}
		buyWR := float64(wc4h.BuyWin) / float64(wc4h.N) * 100
		selWR := float64(wc4h.SelWin) / float64(wc4h.N) * 100
		best := buyWR
		if selWR > best {
			best = selWR
		}
		outRows = append(outRows, OutRow{cond, data, best})
	}

	sort.Slice(outRows, func(i, j int) bool {
		return outRows[i].maxWR > outRows[j].maxWR
	})

	fmt.Printf("sl_mult|h1_regime|m15_regime|m5_regime|m5_ema|m5_rsi|m5_adx|m5_mom")
	for _, w := range windows {
		fmt.Printf("|n_%dm|buy_%dm|sell_%dm", w, w, w)
	}
	fmt.Println()

	for _, r := range outRows {
		c := r.cond
		fmt.Printf("%.1f|%s|%s|%s|%s|%s|%s|%s",
			slMult, c.H1Regime, c.M15Regime, c.M5Regime,
			c.M5EMA, c.M5RSI, c.M5ADX, c.M5Momentum)
		for _, w := range windows {
			wc := r.data[w]
			if wc == nil || wc.N == 0 {
				fmt.Print("|0|-|-")
				continue
			}
			fmt.Printf("|%d|%.1f%%|%.1f%%",
				wc.N,
				float64(wc.BuyWin)/float64(wc.N)*100,
				float64(wc.SelWin)/float64(wc.N)*100)
		}
		fmt.Println()
	}
}

func classRegime(r string) string {
	if r == "" {
		return "unknown"
	}
	return r
}

func classEMA(fast, slow float64) string {
	if fast > slow {
		return "bull"
	}
	return "bear"
}

func classRSI(rsi float64) string {
	switch {
	case rsi >= 60:
		return "high"
	case rsi >= 50:
		return "mid"
	default:
		return "low"
	}
}

func classADX(adx float64) string {
	if adx >= 25 {
		return "trending"
	}
	return "weak"
}

func classMomentum(m string) string {
	if m == "" {
		return "unknown"
	}
	return m
}

func stateID(parsed map[string]map[string]string, period string) string {
	if p, ok := parsed[period]; ok {
		return p["id"]
	}
	return ""
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
