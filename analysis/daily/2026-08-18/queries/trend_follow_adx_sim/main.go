// Follow-up to trend_follow_moderate_sim, same day. That test showed
// blocking M5 "ranging" doesn't help (75.5% win rate vs relaxed's 84.2%) —
// the regime label isn't what distinguished the 4 real losing trades.
//
// What was different in the real losing episode (per checked_market_states):
// M5 ADX was falling continuously through the whole entry window — 28.1 ->
// 26.3 -> 25.5 -> ... -> 11.9, and stayed weak (11-12) through the second
// batch too. A falling ADX means momentum is dying, which a static regime
// label doesn't capture. This tests: on top of the relaxed gate (already
// shipped, already the best performer), add a filter requiring M5 ADX not
// be declining over the last 3 M5 bars (15min) -- does that improve win
// rate over relaxed's 84.2% baseline, and does it specifically avoid the
// 4 real losing entries?
//
// Same real-cooldown simulation, 24h resolution window, same construction
// as trend_follow_moderate_sim for direct comparability.
//
// Run: DATABASE_URL=postgres://...@localhost:5433/tbot_prod go run main.go
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

const minATR = 1e-6
const winMin = 1440
const adxLookbackBars = 3 // 15min on M5

type Candle struct {
	BarTime         time.Time
	Open, High, Low float64
}

type MS struct {
	Regime           string
	EMAFast, EMASlow float64
	ADX, ATR         float64
	Close            float64
}

type ADXPoint struct {
	BarTime time.Time
	ADX     float64
}

type SigMoment struct {
	SymbolID  string
	CreatedAt time.Time
	M5ID      string
	M15ID     string
	H1ID      string
}

func simulate(sc []Candle, start, endTime time.Time, entry, tp, sl float64, isBuy bool) (int, time.Time) {
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
			tpHit, slHit = c.High >= tp, c.Low <= sl
		} else {
			tpHit, slHit = c.Low <= tp, c.High >= sl
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

// adxDeclining checks whether M5 ADX at time t is lower than it was
// adxLookbackBars M5 bars earlier for this symbol.
func adxDeclining(series []ADXPoint, t time.Time) bool {
	idx := sort.Search(len(series), func(i int) bool { return series[i].BarTime.After(t) }) - 1
	if idx < adxLookbackBars {
		return false
	}
	return series[idx].ADX < series[idx-adxLookbackBars].ADX
}

type result struct {
	matched, skippedCooldown, win, loss, unresolved int
	fastLosses                                      int
}

// mode: "relaxed", "relaxed_adx_filter"
func runVariant(moments []SigMoment, msMap map[string]MS, candleMap map[string][]Candle, adxSeries map[string][]ADXPoint, mode string) result {
	const consecutiveFailsToCooldown = 2
	const cooldownDuration = 60 * time.Minute
	type cdState struct {
		failStreak    int
		cooldownUntil time.Time
	}
	cooldown := map[string]*cdState{}
	var r result

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
		// relaxed regime gate (already shipped, best performer so far)
		if dir == "BUY" && m5.Regime == "trending_down" {
			continue
		}
		if dir == "SELL" && m5.Regime == "trending_up" {
			continue
		}

		if mode == "relaxed_adx_filter" {
			if adxDeclining(adxSeries[mo.SymbolID], mo.CreatedAt) {
				continue
			}
		}

		if h1.ADX < 20 {
			continue
		}
		m15, ok := msMap[mo.M15ID]
		if !ok || m15.ATR <= 0 {
			continue
		}

		cdKey := mo.SymbolID + "|" + dir
		if cd := cooldown[cdKey]; cd != nil && mo.CreatedAt.Before(cd.cooldownUntil) {
			r.skippedCooldown++
			continue
		}
		r.matched++

		entry := m5.Close
		var tp, sl float64
		if dir == "BUY" {
			sl, tp = entry-1.5*m15.ATR, entry+2.5*m15.ATR
		} else {
			sl, tp = entry+1.5*m15.ATR, entry-2.5*m15.ATR
		}
		endTime := mo.CreatedAt.Add(winMin * time.Minute)
		outcome, closeTime := simulate(candleMap[mo.SymbolID], mo.CreatedAt, endTime, entry, tp, sl, dir == "BUY")

		cd := cooldown[cdKey]
		if cd == nil {
			cd = &cdState{}
			cooldown[cdKey] = cd
		}
		switch outcome {
		case -1:
			r.loss++
			cd.failStreak++
			if cd.failStreak >= consecutiveFailsToCooldown {
				cd.cooldownUntil = closeTime.Add(cooldownDuration)
			}
			if closeTime.Sub(mo.CreatedAt) <= 15*time.Minute {
				r.fastLosses++
			}
		case 1:
			r.win++
			cd.failStreak = 0
		default:
			r.unresolved++
		}
	}
	return r
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: trend_follow_adx_sim <postgres-url>")
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
			SELECT id::text, COALESCE(regime,''), COALESCE(ema_fast,0), COALESCE(ema_slow,0), COALESCE(adx,0), COALESCE(atr,0), COALESCE(close,0)
			FROM market_states WHERE period IN ('M5','M15','H1')`)
		must(err)
		for rows.Next() {
			var id string
			var m MS
			must(rows.Scan(&id, &m.Regime, &m.EMAFast, &m.EMASlow, &m.ADX, &m.ATR, &m.Close))
			msMap[id] = m
		}
		rows.Close()
	}

	logf("loading M5 ADX time series per symbol...")
	adxSeries := map[string][]ADXPoint{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT symbol_id::text, bar_time, COALESCE(adx,0)
			FROM market_states WHERE period='M5' AND provider='ctrader' ORDER BY symbol_id, bar_time ASC`)
		must(err)
		for rows.Next() {
			var sid string
			var p ADXPoint
			must(rows.Scan(&sid, &p.BarTime, &p.ADX))
			adxSeries[sid] = append(adxSeries[sid], p)
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
	logf("  -> %d unique M5 moments total\n", len(moments))

	relaxed := runVariant(moments, msMap, candleMap, adxSeries, "relaxed")
	adxFiltered := runVariant(moments, msMap, candleMap, adxSeries, "relaxed_adx_filter")

	print := func(name string, r result) {
		total := r.win + r.loss
		wr := 0.0
		if total > 0 {
			wr = 100 * float64(r.win) / float64(total)
		}
		fastPct := 0.0
		if r.loss > 0 {
			fastPct = 100 * float64(r.fastLosses) / float64(r.loss)
		}
		fmt.Printf("%-20s matched=%-5d skipped_cooldown=%-4d win=%-4d loss=%-4d unresolved=%-4d win_rate=%.1f%%  fast_losses(<=15min)=%d/%d (%.0f%%)\n",
			name, r.matched, r.skippedCooldown, r.win, r.loss, r.unresolved, wr, r.fastLosses, r.loss, fastPct)
	}
	print("relaxed", relaxed)
	print("relaxed_adx_filter", adxFiltered)
}

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
