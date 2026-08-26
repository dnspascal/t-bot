package main

// Part 8 of analysis/daily/2026-08-17/report.html.
//
// Tests relaxing trend_follow's M5-regime-confirmation gate. Today's
// hold_reason_sim showed H1 trended for 144 of 231 XAUUSD bars, but M5 never
// once agreed with it on the same bar — split roughly evenly between M5's EMA
// disagreeing (69x) and M5's regime label not being the exact trend string
// even though EMA already agreed (75x). This tests only relaxing the second
// check: instead of requiring m5.Regime == the trend direction, only reject
// if m5.Regime == the OPPOSITE direction (allow ranging/breakout through).
// The EMA check — the one actually tied to the original bug this gate was
// added to fix (commit 5b6880d, 2026-08-03: M5 pullback entries triggered
// instant ema_cross_against+regime_against+rsi_against closes, 61 trades
// closed in 5min on a single Friday) — is left fully intact in both variants.
//
// Also checks whether relaxation reintroduces that original problem: for
// every simulated loss, was it closed suspiciously fast (<=15min, matching
// the original bug's signature) — a proxy for "the watcher would likely have
// force-closed this immediately for the same reason as before."
//
// Run: DATABASE_URL=postgres://...@localhost:5433/tbot_prod go run main.go
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
const winMin = 1440 // 24h — trend_follow's M15-ATR-based stops (1.5x/2.5x) are much
// wider than dd_ranging_breakout's M5-ATR ones (1.0x/1.5x); a 4h window left 43-56%
// of trades unresolved here (vs 1.0% for dd_ranging_breakout at 4h), which silently
// drops them from the win-rate denominator and invalidates the comparison.

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

// simulate returns (outcome, closeTime): outcome is 1=win, -1=loss, 0=unresolved.
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
	fastLosses                                      int // losses closed <=15min after entry
}

func runVariant(moments []SigMoment, msMap map[string]MS, candleMap map[string][]Candle, relaxed bool) result {
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
		// EMA check — unchanged in both variants, this is what actually guards
		// against the original bug (M5 actively against the trade direction).
		if dir == "BUY" && m5.EMAFast < m5.EMASlow {
			continue
		}
		if dir == "SELL" && m5.EMAFast > m5.EMASlow {
			continue
		}
		// Regime check — the one being tested.
		if relaxed {
			if dir == "BUY" && m5.Regime == "trending_down" {
				continue
			}
			if dir == "SELL" && m5.Regime == "trending_up" {
				continue
			}
		} else {
			if dir == "BUY" && m5.Regime != "trending_up" {
				continue
			}
			if dir == "SELL" && m5.Regime != "trending_down" {
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
		fmt.Fprintln(os.Stderr, "usage: trend_follow_relaxed_sim <postgres-url>")
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
		rows, err := db.QueryContext(ctx, `SELECT symbol_id::text, created_at, checked_market_states FROM signals WHERE checked_market_states IS NOT NULL AND created_at >= now() - interval '30 days' ORDER BY created_at ASC`)
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

	strict := runVariant(moments, msMap, candleMap, false)
	relaxed := runVariant(moments, msMap, candleMap, true)

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
		fmt.Printf("%-8s matched=%-4d skipped_cooldown=%-4d win=%-4d loss=%-4d unresolved=%-4d win_rate=%.1f%%  fast_losses(<=15min)=%d/%d (%.0f%%)\n",
			name, r.matched, r.skippedCooldown, r.win, r.loss, r.unresolved, wr, r.fastLosses, r.loss, fastPct)
	}
	print("strict", strict)
	print("relaxed", relaxed)
}

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
