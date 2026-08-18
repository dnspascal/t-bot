// Follow-up to 2026-08-17's trend_follow_relaxed_sim. That fix shipped as
// "M5 regime must not be trending_down" (relaxed) instead of "M5 regime
// must equal trending_up" (strict). Four real trades since deployment all
// lost money, and checked_market_states showed why: relaxed fires a BUY
// signal on nearly every M5 bar for hours during any H1 uptrend, because
// M5 regime reads "ranging" or "breakout" almost continuously during a
// slow grind — never "trending_down" — so the relaxed gate is satisfied
// on nearly every bar, not just genuinely-confirming ones.
//
// This tests a third variant: "moderate" = M5 regime must be the trend
// direction OR "breakout" (blocks "ranging", the label doing the actual
// damage; still allows breakout-regime M5 bars through, which is the part
// of the relaxation that was legitimate). Compares all three variants
// side by side: does moderate still solve the original zero-fire problem,
// while cutting down the continuous-signal problem and improving win rate?
//
// Same real-cooldown simulation and 24h resolution window as yesterday's
// tool, for direct comparability.
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
	fastLosses                                      int
}

// mode: "strict", "relaxed", "moderate"
func runVariant(moments []SigMoment, msMap map[string]MS, candleMap map[string][]Candle, mode string) result {
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

		switch mode {
		case "strict":
			if dir == "BUY" && m5.Regime != "trending_up" {
				continue
			}
			if dir == "SELL" && m5.Regime != "trending_down" {
				continue
			}
		case "relaxed":
			if dir == "BUY" && m5.Regime == "trending_down" {
				continue
			}
			if dir == "SELL" && m5.Regime == "trending_up" {
				continue
			}
		case "moderate":
			if dir == "BUY" && !(m5.Regime == "trending_up" || m5.Regime == "breakout") {
				continue
			}
			if dir == "SELL" && !(m5.Regime == "trending_down" || m5.Regime == "breakout") {
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
		fmt.Fprintln(os.Stderr, "usage: trend_follow_moderate_sim <postgres-url>")
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

	strict := runVariant(moments, msMap, candleMap, "strict")
	relaxed := runVariant(moments, msMap, candleMap, "relaxed")
	moderate := runVariant(moments, msMap, candleMap, "moderate")

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
		fmt.Printf("%-10s matched=%-5d skipped_cooldown=%-4d win=%-4d loss=%-4d unresolved=%-4d win_rate=%.1f%%  fast_losses(<=15min)=%d/%d (%.0f%%)\n",
			name, r.matched, r.skippedCooldown, r.win, r.loss, r.unresolved, wr, r.fastLosses, r.loss, fastPct)
	}
	print("strict", strict)
	print("relaxed", relaxed)
	print("moderate", moderate)
}

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
