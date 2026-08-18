// Tests whether adding an M5 ADX floor to dd_ranging_breakout would have
// helped. Real cause behind two 2026-08-18 losses (-$4.09, -$4.33): the
// strategy checks m15.ADX < 20 but its actual entry trigger is m5.Regime ==
// "breakout" — an M5-level label never checked against M5 ADX. Both losing
// entries fired at M5 ADX 15.0-15.2, well below any real-trend threshold.
//
// Same shape of bug already fixed in ema_pullback (2026-08-13) and already
// tested (and rejected) as a trend_follow fix earlier today — an ADX floor
// is a plausible-sounding fix that has failed backtesting before, so this
// gets the same full-history treatment rather than shipping on pattern
// match alone.
//
// Reconstructs dd_ranging_breakout's exact real entry logic (H1 ranging,
// M5 breakout regime + bullish EMA + RSI>=50, M15 ADX>=20, M15 not
// trending_down), with a real cooldown simulation matching the strategy's
// own OnClosed (2 consecutive invalidated closes -> 60min pause), SL/TP at
// the strategy's real 1.0x/1.5x M5 ATR construction.
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
const slATRMult = 1.0
const tpATRMult = 1.5

type Candle struct {
	BarTime         time.Time
	Open, High, Low float64
}

type MS struct {
	Regime           string
	EMAFast, EMASlow float64
	RSI, ADX, ATR    float64
	Close            float64
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

type result struct {
	matched, skippedCooldown, win, loss, unresolved int
}

func (r result) winRate() float64 {
	total := r.win + r.loss
	if total == 0 {
		return 0
	}
	return 100 * float64(r.win) / float64(total)
}
func (r result) expectancyR() float64 {
	total := r.win + r.loss
	if total == 0 {
		return 0
	}
	return (tpATRMult/slATRMult*float64(r.win) - float64(r.loss)) / float64(total)
}

// m5ADXFloor: 0 means no filter (current live behavior).
func runVariant(moments []SigMoment, msMap map[string]MS, candleMap map[string][]Candle, m5ADXFloor float64) result {
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
		if m5ADXFloor > 0 && m5.ADX < m5ADXFloor {
			continue
		}
		m15, ok := msMap[mo.M15ID]
		if !ok || m15.ADX < 20 {
			continue
		}
		if m15.Regime == "trending_down" {
			continue
		}

		cdKey := mo.SymbolID
		if cd := cooldown[cdKey]; cd != nil && mo.CreatedAt.Before(cd.cooldownUntil) {
			r.skippedCooldown++
			continue
		}
		r.matched++

		entry := m5.Close
		sl := entry - slATRMult*m5.ATR
		tp := entry + tpATRMult*m5.ATR
		endTime := mo.CreatedAt.Add(winMin * time.Minute)
		outcome, closeTime := simulate(candleMap[mo.SymbolID], mo.CreatedAt, endTime, entry, tp, sl, true)

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
		fmt.Fprintln(os.Stderr, "usage: dd_ranging_m5adx_sim <postgres-url>")
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
			SELECT id::text, COALESCE(regime,''), COALESCE(ema_fast,0), COALESCE(ema_slow,0), COALESCE(rsi,0), COALESCE(adx,0), COALESCE(atr,0), COALESCE(close,0)
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
	logf("  -> %d unique M5 moments total\n", len(moments))

	print := func(name string, r result) {
		fmt.Printf("%-20s matched=%-5d skipped_cooldown=%-4d win=%-4d loss=%-4d unresolved=%-4d win_rate=%.1f%%  expectancy=%.2fR\n",
			name, r.matched, r.skippedCooldown, r.win, r.loss, r.unresolved, r.winRate(), r.expectancyR())
	}
	print("baseline (no M5 ADX)", runVariant(moments, msMap, candleMap, 0))
	print("M5 ADX >= 15", runVariant(moments, msMap, candleMap, 15))
	print("M5 ADX >= 20", runVariant(moments, msMap, candleMap, 20))
	print("M5 ADX >= 25", runVariant(moments, msMap, candleMap, 25))
}

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
