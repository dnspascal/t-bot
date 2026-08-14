package main

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
const slMult = 0.5

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
	CreatedAt time.Time // when the signal was generated (≈ M5 bar close time)
	M5ID      string
	M15ID     string
	H1ID      string
}

type Cond struct {
	H1Regime   string // trending_up / trending_down / ranging / breakout / unknown
	M15Regime  string
	M5Regime   string
	M5EMA      string // bull / bear
	M5RSI      string // high (≥60) / mid (50-59) / low (<50)
	M5ADX      string // trending (≥25) / weak (<25)
	M5Momentum string // rising / stable / falling / unknown
}

type WinCount struct {
	N       int // total signal moments in this cell
	BuyWin  int // price hit BUY TP before BUY SL within window
	BuyLoss int // price hit BUY SL before BUY TP within window
	SelWin  int // price hit SELL TP before SELL SL within window
	SelLoss int // price hit SELL SL before SELL TP within window
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: stratanalysis <postgres-url>")
		os.Exit(1)
	}

	db, err := sql.Open("pgx", os.Args[1])
	must(err)
	defer db.Close()

	ctx := context.Background()

	logf("loading M5 candles...")
	candleMap := map[string][]Candle{} // symbol_id -> []Candle sorted ASC
	{
		rows, err := db.QueryContext(ctx, `
			SELECT symbol_id::text, bar_time, open, high, low
			FROM candles
			WHERE period = 'M5'
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
	msMap := map[string]MS{} // id (uuid as string) -> MS
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
				continue // no M5 state attached — cannot analyse
			}

			// Deduplicate: each M5 bar should contribute exactly one data point
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

	type GroupData = map[int]*WinCount
	results := map[Cond]GroupData{}

	skipped := 0
	for _, m := range moments {
		m5, ok := msMap[m.M5ID]
		if !ok || m5.ATR < minATR || m5.Close == 0 {
			skipped++
			continue
		}

		m15 := msMap[m.M15ID] // zero-value MS (regime="") if ID empty or not found
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
		startIdx := sort.Search(len(sc), func(i int) bool {
			return sc[i].BarTime.After(m.CreatedAt)
		})

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

				// SELL: mirror logic
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

	logf("done. skipped=%d (no M5 state or ATR=0)", skipped)

	type OutRow struct {
		cond  Cond
		data  GroupData
		maxWR float64 // best win rate across both directions at 4h window — for sorting
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

	fmt.Print("h1_regime|m15_regime|m5_regime|m5_ema|m5_rsi|m5_adx|m5_mom")
	for _, w := range windows {
		fmt.Printf("|n_%dm|buy_%dm|sell_%dm", w, w, w)
	}
	fmt.Println()

	for _, r := range outRows {
		c := r.cond
		fmt.Printf("%s|%s|%s|%s|%s|%s|%s",
			c.H1Regime, c.M15Regime, c.M5Regime,
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
