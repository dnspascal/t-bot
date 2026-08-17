package main

// Checks whether dd_ranging_breakout's "M15 disagreement predicts loss" pattern
// (found by multivar_scan) generalizes to dd_oversold_bounce, using its exact
// live condition shape (H1=trending_down, M5=trending_down|breakout, EMA bear,
// RSI<50, ADX>=25 — a BUY-the-dip-in-a-downtrend thesis, the opposite relationship
// between trade direction and trend from dd_ranging_breakout's trend-continuation
// thesis). Result: the pattern flips — M15 trending_down is dd_oversold_bounce's
// BEST bucket (73.1%), not its worst. See report Part 2.
// (every historical M5 moment matching dd_ranging_breakout's exact live
// condition shape), same M1-candle forward simulation to 4h, but tallies
// every candidate variable in a single pass instead of just RSI.

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
const slMult = 1.0
const minATR = 1e-6
const winMin = 240

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
	BarTime time.Time
}

type SigMoment struct {
	SymbolID  string
	CreatedAt time.Time
	M5ID      string
	M15ID     string
	M30ID     string
	H1ID      string
}

type bucket struct{ n, win, loss int }

func (b *bucket) winRate() float64 {
	if b.n == 0 {
		return 0
	}
	return 100 * float64(b.win) / float64(b.n)
}

var nairobi *time.Location

func rsiBucket(rsi float64) string {
	switch {
	case rsi >= 90:
		return "5_90-100"
	case rsi >= 80:
		return "4_80-90"
	case rsi >= 70:
		return "3_70-80"
	case rsi >= 60:
		return "2_60-70"
	default:
		return "1_50-60"
	}
}

func adxBucket(adx float64) string {
	switch {
	case adx >= 40:
		return "5_40+"
	case adx >= 35:
		return "4_35-40"
	case adx >= 30:
		return "3_30-35"
	case adx >= 25:
		return "2_25-30"
	default:
		return "1_<25"
	}
}

func hourBucket(t time.Time) string {
	h := t.In(nairobi).Hour()
	return fmt.Sprintf("%02d:00-%02d:00", h, (h+1)%24)
}

func weekdayBucket(t time.Time) string {
	return t.In(nairobi).Weekday().String()
}

func h1FreshnessBucket(signalTime, h1BarTime time.Time) string {
	if h1BarTime.IsZero() {
		return "unknown"
	}
	mins := signalTime.Sub(h1BarTime).Minutes()
	switch {
	case mins < 15:
		return "1_0-15min_since_h1_bar_time"
	case mins < 30:
		return "2_15-30min_since_h1_bar_time"
	case mins < 45:
		return "3_30-45min_since_h1_bar_time"
	case mins < 60:
		return "4_45-60min_since_h1_bar_time"
	case mins < 120:
		return "5_60-120min_since_h1_bar_time"
	default:
		return "6_120min-plus_since_h1_bar_time"
	}
}

func m15RegimeBucket(r string) string {
	if r == "" {
		return "unknown"
	}
	return r
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: multivar_scan <postgres-url>")
		os.Exit(1)
	}
	nairobi, _ = time.LoadLocation("Africa/Nairobi")

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
			       COALESCE(rsi,0), COALESCE(adx,0), COALESCE(atr,0), COALESCE(close,0), bar_time
			FROM market_states WHERE period IN ('M5','M15','M30','H1')`)
		must(err)
		for rows.Next() {
			var id string
			var m MS
			must(rows.Scan(&id, &m.Regime, &m.EMAFast, &m.EMASlow, &m.RSI, &m.ADX, &m.ATR, &m.Close, &m.BarTime))
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
			moments = append(moments, SigMoment{symID, t, m5id, parsed["M15"]["id"], parsed["M30"]["id"], parsed["H1"]["id"]})
		}
		rows.Close()
	}
	logf("  → %d unique M5 moments total", len(moments))

	dims := map[string]map[string]*bucket{
		"RSI":            {},
		"ADX":            {},
		"Hour":           {},
		"Weekday":        {},
		"H1_freshness":   {},
		"M15_regime":     {},
		"M30_regime":     {},
		"ATR_quartile":   {},
	}

	type matchedMoment struct {
		mo         SigMoment
		m5, h1     MS
		m15, m30   MS
	}
	var matches []matchedMoment

	for _, mo := range moments {
		m5, ok := msMap[mo.M5ID]
		if !ok || m5.ATR < minATR || m5.Close == 0 {
			continue
		}
		h1 := msMap[mo.H1ID]
		// dd_oversold_bounce's exact live condition shape (BUY-only bounce-in-downtrend):
		if h1.Regime != "trending_down" {
			continue
		}
		if m5.Regime != "trending_down" && m5.Regime != "breakout" {
			continue
		}
		if m5.EMAFast >= m5.EMASlow {
			continue
		}
		if m5.RSI >= 50 {
			continue
		}
		if m5.ADX < 25 {
			continue
		}
		m15 := msMap[mo.M15ID]
		m30 := msMap[mo.M30ID]
		matches = append(matches, matchedMoment{mo, m5, h1, m15, m30})
	}
	logf("  → %d moments matched dd_oversold_bounce's condition shape", len(matches))

	{
		var minM, maxM, sumM float64
		minM = 1e18
		zeroH1 := 0
		for _, mm := range matches {
			if mm.h1.BarTime.IsZero() {
				zeroH1++
				continue
			}
			mins := mm.mo.CreatedAt.Sub(mm.h1.BarTime).Minutes()
			if mins < minM {
				minM = mins
			}
			if mins > maxM {
				maxM = mins
			}
			sumM += mins
		}
		n := len(matches) - zeroH1
		if n > 0 {
			logf("  H1 freshness raw stats: min=%.1fmin max=%.1fmin avg=%.1fmin (zeroH1=%d)", minM, maxM, sumM/float64(n), zeroH1)
		}
	}

	// ATR quartiles computed across the matched population itself.
	atrs := make([]float64, len(matches))
	for i, mm := range matches {
		atrs[i] = mm.m5.ATR
	}
	sortedATR := append([]float64{}, atrs...)
	sort.Float64s(sortedATR)
	q1 := sortedATR[len(sortedATR)/4]
	q2 := sortedATR[len(sortedATR)/2]
	q3 := sortedATR[3*len(sortedATR)/4]
	atrBucket := func(v float64) string {
		switch {
		case v <= q1:
			return "1_lowest_quartile"
		case v <= q2:
			return "2_second_quartile"
		case v <= q3:
			return "3_third_quartile"
		default:
			return "4_highest_quartile"
		}
	}

	for _, mm := range matches {
		mo, m5, h1, m15, m30 := mm.mo, mm.m5, mm.h1, mm.m15, mm.m30

		entry := m5.Close
		tp := entry + tpMult*m5.ATR
		sl := entry - slMult*m5.ATR

		sc := candleMap[mo.SymbolID]
		startIdx := sort.Search(len(sc), func(i int) bool { return !sc[i].BarTime.Before(mo.CreatedAt) })
		if startIdx > 0 {
			startIdx--
		}
		endTime := mo.CreatedAt.Add(winMin * time.Minute)

		outcome := 0 // 0=unresolved, 1=win, -1=loss
		for i := startIdx; i < len(sc); i++ {
			c := sc[i]
			if c.BarTime.After(endTime) {
				break
			}
			tpHit := c.High >= tp
			slHit := c.Low <= sl
			if tpHit && slHit {
				if (c.High - entry) >= (entry - c.Low) {
					outcome = 1
				} else {
					outcome = -1
				}
				break
			} else if tpHit {
				outcome = 1
				break
			} else if slHit {
				outcome = -1
				break
			}
		}

		record := func(dim, key string) {
			if dims[dim][key] == nil {
				dims[dim][key] = &bucket{}
			}
			b := dims[dim][key]
			b.n++
			if outcome == 1 {
				b.win++
			} else if outcome == -1 {
				b.loss++
			}
		}

		record("RSI", rsiBucket(m5.RSI))
		record("ADX", adxBucket(m5.ADX))
		record("Hour", hourBucket(mo.CreatedAt))
		record("Weekday", weekdayBucket(mo.CreatedAt))
		record("H1_freshness", h1FreshnessBucket(mo.CreatedAt, h1.BarTime))
		record("M15_regime", m15RegimeBucket(m15.Regime))
		record("M30_regime", m15RegimeBucket(m30.Regime))
		record("ATR_quartile", atrBucket(m5.ATR))
	}

	dimOrder := []string{"RSI", "ADX", "H1_freshness", "ATR_quartile", "M15_regime", "M30_regime", "Hour", "Weekday"}
	for _, dim := range dimOrder {
		fmt.Printf("\n=== %s ===\n", dim)
		var keys []string
		for k := range dims[dim] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b := dims[dim][k]
			fmt.Printf("  %-24s n=%-5d win=%-4d loss=%-4d win_rate=%.1f%%\n", k, b.n, b.win, b.loss, b.winRate())
		}
	}
}

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
