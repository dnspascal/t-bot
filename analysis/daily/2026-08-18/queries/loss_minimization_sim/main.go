// Tests two genuine current-trade loss-minimization levers, as opposed to
// a portfolio-level circuit breaker (which is prevention/avoidance, not
// loss reduction on a trade already open — explicitly not what's being
// tested here). Both levers use only price data, which is the only thing
// that updates faster than once per M5 bar; the reversal watcher's inputs
// (M5 regime/RSI/EMA labels) don't refresh faster than the M5 candle does,
// so checking it more often wouldn't change anything — ruled out for that
// reason rather than assumed to help.
//
// Uses trend_follow's real relaxed-gate signal set (same detection as
// trend_follow_relaxed_sim/trend_follow_moderate_sim from 2026-08-18) since
// it's the best-characterized, largest real-sample strategy in this
// investigation. Both levers are full-path M1 simulations (not just
// first-touch), so a trade that would have eventually hit TP but dipped
// through a tighter threshold first is correctly counted as cut short —
// tightening isn't free, and this is what actually measures the cost.
//
//	sl_sweep   — same relaxed entries, TP fixed at 2.5x M15 ATR, SL swept
//	  across {1.5x (current), 1.2x, 1.0x, 0.75x} M15 ATR. Reports win rate
//	  AND dollar expectancy per trade (not just R-multiple, since R itself
//	  shrinks as SL tightens — dollar terms is the fair comparison).
//
//	peak_giveback — independent of the fixed SL/TP: track the position's
//	  running peak favorable price from entry: once ANY peak gain exists
//	  (no 70% gate, unlike the live peakDrawbackGatePct), close if price
//	  gives back Y% of that peak. Swept across Y in {40%, 50%, 60%}, still
//	  bounded by the original SL/TP as a backstop.
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
const tpATRMult = 2.5

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

// outcome: 1=win, -1=loss, 0=unresolved. exitPrice is where the trade closed.
func simulateSLSweep(sc []Candle, start, endTime time.Time, entry, tp, sl float64, isBuy bool) (outcome int, exitPrice float64) {
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
					return 1, tp
				}
				return -1, sl
			}
			if (entry - c.Low) >= (c.High - entry) {
				return 1, tp
			}
			return -1, sl
		} else if tpHit {
			return 1, tp
		} else if slHit {
			return -1, sl
		}
	}
	return 0, 0
}

// simulatePeakGiveback walks bar-by-bar tracking running peak favorable
// price; closes as soon as price gives back givebackPct of that peak gain,
// bounded by the original SL/TP as a backstop. minPeakGain floors how much
// favorable movement must exist before the giveback rule can engage at all —
// without it, givebackPct=1.0 doesn't mean "no cut", it means "exit the
// instant price ticks back to entry after even a one-cent favorable wiggle",
// which is the most aggressive possible trigger, not the least. Mirrors the
// real peakDrawbackPct's minPeakGain floor in internal/bot/watcher.go.
func simulatePeakGiveback(sc []Candle, start, endTime time.Time, entry, tp, sl float64, isBuy bool, givebackPct, minPeakGain float64) (outcome int, exitPrice float64) {
	startIdx := sort.Search(len(sc), func(i int) bool { return !sc[i].BarTime.Before(start) })
	if startIdx > 0 {
		startIdx--
	}
	peak := entry
	for i := startIdx; i < len(sc); i++ {
		c := sc[i]
		if c.BarTime.After(endTime) {
			break
		}
		// original SL/TP backstop first (can't give back more than the hard stop allows)
		var tpHit, slHit bool
		if isBuy {
			tpHit, slHit = c.High >= tp, c.Low <= sl
		} else {
			tpHit, slHit = c.Low <= tp, c.High >= sl
		}
		if slHit && (!tpHit || (isBuy && (entry-c.Low) >= (c.High-entry)) || (!isBuy && (c.High-entry) >= (entry-c.Low))) {
			return -1, sl
		}
		if tpHit {
			return 1, tp
		}

		if isBuy {
			if c.High > peak {
				peak = c.High
			}
			peakGain := peak - entry
			if peakGain >= minPeakGain {
				giveback := peak - c.Low
				if giveback >= givebackPct*peakGain {
					exit := peak - givebackPct*peakGain
					if exit > entry {
						return 1, exit
					}
					return -1, exit
				}
			}
		} else {
			if c.Low < peak {
				peak = c.Low
			}
			peakGain := entry - peak
			if peakGain >= minPeakGain {
				giveback := c.High - peak
				if giveback >= givebackPct*peakGain {
					exit := peak + givebackPct*peakGain
					if exit < entry {
						return 1, exit
					}
					return -1, exit
				}
			}
		}
	}
	return 0, 0
}

func detectRelaxedMoments(moments []SigMoment, msMap map[string]MS) []struct {
	mo  SigMoment
	dir string
	m5  MS
	m15 MS
} {
	var out []struct {
		mo  SigMoment
		dir string
		m5  MS
		m15 MS
	}
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
		if dir == "BUY" && m5.Regime == "trending_down" {
			continue
		}
		if dir == "SELL" && m5.Regime == "trending_up" {
			continue
		}
		if h1.ADX < 20 {
			continue
		}
		m15, ok := msMap[mo.M15ID]
		if !ok || m15.ATR <= 0 {
			continue
		}
		out = append(out, struct {
			mo  SigMoment
			dir string
			m5  MS
			m15 MS
		}{mo, dir, m5, m15})
	}
	return out
}

type result struct {
	matched, win, loss, unresolved int
	totalWinAmt, totalLossAmt      float64 // sum of |exit-entry| in price units
}

func (r result) winRate() float64 {
	total := r.win + r.loss
	if total == 0 {
		return 0
	}
	return 100 * float64(r.win) / float64(total)
}
func (r result) avgWin() float64 {
	if r.win == 0 {
		return 0
	}
	return r.totalWinAmt / float64(r.win)
}
func (r result) avgLoss() float64 {
	if r.loss == 0 {
		return 0
	}
	return r.totalLossAmt / float64(r.loss)
}
func (r result) expectancy() float64 {
	total := r.win + r.loss
	if total == 0 {
		return 0
	}
	return (r.totalWinAmt - r.totalLossAmt) / float64(total)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: loss_minimization_sim <postgres-url>")
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
	entries := detectRelaxedMoments(moments, msMap)
	logf("  -> %d relaxed-gate trend_follow entries detected\n", len(entries))

	print := func(name string, r result, skipped int) {
		fmt.Printf("%-16s matched=%-5d skipped_cooldown=%-4d win=%-4d loss=%-4d unresolved=%-4d win_rate=%.1f%%  avg_win=$%.2f avg_loss=$%.2f expectancy=$%.2f/trade\n",
			name, r.matched, skipped, r.win, r.loss, r.unresolved, r.winRate(), r.avgWin(), r.avgLoss(), r.expectancy())
	}

	// Same 2-consecutive-fail / 60min per-symbol-per-direction cooldown as
	// trend_follow_relaxed_sim — omitting it (as an earlier version of this
	// tool did) lets every bar of a multi-hour signal streak count as a
	// separate trade, which is the exact over-signaling failure mode this
	// whole investigation started from. Without it these numbers are not
	// comparable to anything else measured today.
	type cdState struct {
		failStreak    int
		cooldownUntil time.Time
	}

	fmt.Println("=== SL tightening sweep (TP fixed at 2.5x M15 ATR) ===")
	for _, slMult := range []float64{1.5, 1.2, 1.0, 0.75} {
		var r result
		skipped := 0
		cooldown := map[string]*cdState{}
		for _, e := range entries {
			cdKey := e.mo.SymbolID + "|" + e.dir
			if cd := cooldown[cdKey]; cd != nil && e.mo.CreatedAt.Before(cd.cooldownUntil) {
				skipped++
				continue
			}
			entry := e.m5.Close
			atr := e.m15.ATR
			isBuy := e.dir == "BUY"
			var tp, sl float64
			if isBuy {
				sl, tp = entry-slMult*atr, entry+tpATRMult*atr
			} else {
				sl, tp = entry+slMult*atr, entry-tpATRMult*atr
			}
			r.matched++
			outcome, exit := simulateSLSweep(candleMap[e.mo.SymbolID], e.mo.CreatedAt, e.mo.CreatedAt.Add(winMin*time.Minute), entry, tp, sl, isBuy)
			cd := cooldown[cdKey]
			if cd == nil {
				cd = &cdState{}
				cooldown[cdKey] = cd
			}
			switch outcome {
			case 1:
				r.win++
				r.totalWinAmt += absf(exit - entry)
				cd.failStreak = 0
			case -1:
				r.loss++
				r.totalLossAmt += absf(exit - entry)
				cd.failStreak++
				if cd.failStreak >= 2 {
					cd.cooldownUntil = e.mo.CreatedAt.Add(60 * time.Minute)
				}
			default:
				r.unresolved++
			}
		}
		print(fmt.Sprintf("SL=%.2gx ATR", slMult), r, skipped)
	}

	fmt.Println("\n=== Peak-giveback early cut (min peak gain = 0.3x ATR, 1.5x/2.5x ATR backstop) ===")
	for _, giveback := range []float64{1.0, 0.6, 0.5, 0.4} {
		var r result
		skipped := 0
		cooldown := map[string]*cdState{}
		for _, e := range entries {
			cdKey := e.mo.SymbolID + "|" + e.dir
			if cd := cooldown[cdKey]; cd != nil && e.mo.CreatedAt.Before(cd.cooldownUntil) {
				skipped++
				continue
			}
			entry := e.m5.Close
			atr := e.m15.ATR
			isBuy := e.dir == "BUY"
			var tp, sl float64
			if isBuy {
				sl, tp = entry-1.5*atr, entry+tpATRMult*atr
			} else {
				sl, tp = entry+1.5*atr, entry-tpATRMult*atr
			}
			r.matched++
			outcome, exit := simulatePeakGiveback(candleMap[e.mo.SymbolID], e.mo.CreatedAt, e.mo.CreatedAt.Add(winMin*time.Minute), entry, tp, sl, isBuy, giveback, 0.3*atr)
			cd := cooldown[cdKey]
			if cd == nil {
				cd = &cdState{}
				cooldown[cdKey] = cd
			}
			switch outcome {
			case 1:
				r.win++
				r.totalWinAmt += absf(exit - entry)
				cd.failStreak = 0
			case -1:
				r.loss++
				r.totalLossAmt += absf(exit - entry)
				cd.failStreak++
				if cd.failStreak >= 2 {
					cd.cooldownUntil = e.mo.CreatedAt.Add(60 * time.Minute)
				}
			default:
				r.unresolved++
			}
		}
		label := "no giveback cut (baseline)"
		if giveback < 1.0 {
			label = fmt.Sprintf("giveback=%.0f%%", giveback*100)
		}
		print(label, r, skipped)
	}
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
