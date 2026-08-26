package main

// For every real trend_follow XAUUSD trade that lost while entering on a
// STALE M5 regime alignment (streak > 2 bars), rewind to the first bar of
// that same regime run (streak == 1 — the moment it was genuinely fresh),
// check whether the entry gate was actually satisfiable there too, and if
// so simulate a hypothetical entry from that fresh bar forward using real
// M1 candles (same TP/SL math as the live strategy: 1.5x/2.5x M15 ATR).
// Compares real outcome (stale entry) against counterfactual outcome
// (fresh entry, same episode) trade by trade.
//
// Run: go run . <postgres-url>
import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type M5State struct {
	BarTime time.Time
	Regime  string
	Close   float64
}
type MS struct {
	Regime           string
	EMAFast, EMASlow float64
	ADX, ATR         float64
}
type Candle struct {
	BarTime         time.Time
	Open, High, Low float64
}
type Trade struct {
	Side      string
	NetProfit float64
	EntryBar  time.Time
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func simulate(sc []Candle, start, endTime time.Time, entry, tp, sl float64, isBuy bool) (int, time.Time) {
	startIdx := sort.Search(len(sc), func(i int) bool { return !sc[i].BarTime.Before(start) })
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

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: counterfactual_fresh_entry <postgres-url>")
		os.Exit(1)
	}
	db, err := sql.Open("pgx", os.Args[1])
	must(err)
	defer db.Close()
	ctx := context.Background()

	m5 := []M5State{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT ms.bar_time, COALESCE(ms.regime,''), COALESCE(ms.close,0)
			FROM market_states ms JOIN symbols s ON s.id = ms.symbol_id
			WHERE s.symbol='XAUUSD' AND ms.period='M5' ORDER BY ms.bar_time ASC`)
		must(err)
		for rows.Next() {
			var st M5State
			must(rows.Scan(&st.BarTime, &st.Regime, &st.Close))
			m5 = append(m5, st)
		}
		rows.Close()
	}

	// Full M5/M15/H1 state snapshots keyed by bar_time, for gate + ATR lookups near a given time.
	loadPeriod := func(period string) map[time.Time]MS {
		out := map[time.Time]MS{}
		rows, err := db.QueryContext(ctx, `
			SELECT ms.bar_time, COALESCE(ms.regime,''), COALESCE(ms.ema_fast,0), COALESCE(ms.ema_slow,0), COALESCE(ms.adx,0), COALESCE(ms.atr,0)
			FROM market_states ms JOIN symbols s ON s.id = ms.symbol_id
			WHERE s.symbol='XAUUSD' AND ms.period=$1 ORDER BY ms.bar_time ASC`, period)
		must(err)
		for rows.Next() {
			var t time.Time
			var m MS
			must(rows.Scan(&t, &m.Regime, &m.EMAFast, &m.EMASlow, &m.ADX, &m.ATR))
			out[t] = m
		}
		rows.Close()
		return out
	}
	h1States := loadPeriod("H1")
	m15States := loadPeriod("M15")

	candles := []Candle{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT c.bar_time, c.open, c.high, c.low
			FROM candles c JOIN symbols s ON s.id = c.symbol_id
			WHERE s.symbol='XAUUSD' AND c.period='M1' ORDER BY c.bar_time ASC`)
		must(err)
		for rows.Next() {
			var c Candle
			must(rows.Scan(&c.BarTime, &c.Open, &c.High, &c.Low))
			candles = append(candles, c)
		}
		rows.Close()
	}
	fmt.Fprintf(os.Stderr, "loaded %d M5 states, %d M1 candles\n", len(m5), len(candles))

	trades := []Trade{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT p.side, f.net_profit, msq.bar_time
			FROM fills f
			JOIN positions p ON p.id = f.our_position_id
			JOIN symbols s ON s.id = f.symbol_id
			LEFT JOIN orders o ON o.id = p.our_order_id
			LEFT JOIN signals sig ON sig.id = o.signal_id
			LEFT JOIN market_states msq ON msq.id::text = (sig.checked_market_states->'M5'->>'id')
			WHERE f.event_type='close' AND sig.strategy='trend_follow' AND s.symbol='XAUUSD'
			  AND msq.bar_time IS NOT NULL`)
		must(err)
		for rows.Next() {
			var t Trade
			must(rows.Scan(&t.Side, &t.NetProfit, &t.EntryBar))
			trades = append(trades, t)
		}
		rows.Close()
	}

	idxFor := func(bt time.Time) int {
		i := sort.Search(len(m5), func(i int) bool { return !m5[i].BarTime.Before(bt) })
		if i < len(m5) && m5[i].BarTime.Equal(bt) {
			return i
		}
		return -1
	}
	nearestMS := func(m map[time.Time]MS, bt time.Time) (MS, bool) {
		if v, ok := m[bt]; ok {
			return v, true
		}
		// fall back to nearest prior key within 65 minutes
		var best time.Time
		found := false
		for k := range m {
			if !k.After(bt) && (!found || k.After(best)) {
				best, found = k, true
			}
		}
		if found && bt.Sub(best) <= 65*time.Minute {
			return m[best], true
		}
		return MS{}, false
	}

	checked, staleLosers, gateValidAtFresh, freshWouldWin, freshWouldLose, freshUnresolved := 0, 0, 0, 0, 0, 0
	var pnlDelta float64

	for _, t := range trades {
		idx := idxFor(t.EntryBar)
		if idx < 0 {
			continue
		}
		checked++
		regime := m5[idx].Regime
		streakStart := idx
		for streakStart > 0 && m5[streakStart-1].Regime == regime {
			streakStart--
		}
		streak := idx - streakStart + 1
		if streak <= 2 || t.NetProfit > 0 {
			continue // only interested in stale losers
		}
		staleLosers++

		freshBar := m5[streakStart]
		h1, ok1 := nearestMS(h1States, freshBar.BarTime)
		m15, ok2 := nearestMS(m15States, freshBar.BarTime)
		if !ok1 || !ok2 || m15.ATR <= 0 {
			continue
		}
		isBuy := t.Side == "BUY"
		// Re-check the entry gate would have fired at the fresh bar too.
		if isBuy && h1.Regime != "trending_up" {
			continue
		}
		if !isBuy && h1.Regime != "trending_down" {
			continue
		}
		if isBuy && h1.ADX < 20 {
			continue
		}
		if !isBuy && h1.ADX < 20 {
			continue
		}
		gateValidAtFresh++

		entry := freshBar.Close
		var tp, sl float64
		if isBuy {
			sl, tp = entry-1.5*m15.ATR, entry+2.5*m15.ATR
		} else {
			sl, tp = entry+1.5*m15.ATR, entry-2.5*m15.ATR
		}
		outcome, _ := simulate(candles, freshBar.BarTime, freshBar.BarTime.Add(24*time.Hour), entry, tp, sl, isBuy)
		switch outcome {
		case 1:
			freshWouldWin++
			// approximate PnL at same $ per pip magnitude as the real trade's risk (rough, for direction only)
		case -1:
			freshWouldLose++
		default:
			freshUnresolved++
		}
	}

	fmt.Printf("\ntrades checked: %d\n", checked)
	fmt.Printf("stale losers (streak>2, net_profit<=0): %d\n", staleLosers)
	fmt.Printf("of those, entry gate also valid at the fresh bar: %d\n", gateValidAtFresh)
	fmt.Printf("  → counterfactual fresh entry would WIN:  %d (%.1f%% of gate-valid)\n", freshWouldWin, pct(freshWouldWin, gateValidAtFresh))
	fmt.Printf("  → counterfactual fresh entry would LOSE: %d (%.1f%%)\n", freshWouldLose, pct(freshWouldLose, gateValidAtFresh))
	fmt.Printf("  → unresolved within 24h: %d (%.1f%%)\n", freshUnresolved, pct(freshUnresolved, gateValidAtFresh))
	_ = pnlDelta
}

func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return 100 * float64(n) / float64(d)
}
