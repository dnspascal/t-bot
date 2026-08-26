package main

// Tests whether trend_follow winners enter on a FRESH M5 regime alignment
// (few consecutive prior bars already showing the same regime) versus losers
// entering on a STALE one (many consecutive prior bars already aligned,
// i.e. the move has likely already run most of its course). If true, this
// is a structural explanation for why lagging-indicator confirmation still
// produces winners (early-in-the-swing entries) alongside losers that barely
// move favorably before reversing (late-in-the-swing entries) — both satisfy
// the exact same entry gate (H1 regime + M5 regime + EMA alignment + ADX),
// which has no way to distinguish early from late within a swing.
//
// Run: DATABASE_URL=postgres://...@localhost:5433/tbot_prod go run main.go <postgres-url>
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
}

type Trade struct {
	Side       string
	NetProfit  float64
	OpenPrice  float64
	MaxFav     float64
	EntryBar   time.Time
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: regime_freshness_at_entry <postgres-url>")
		os.Exit(1)
	}
	db, err := sql.Open("pgx", os.Args[1])
	must(err)
	defer db.Close()
	ctx := context.Background()

	// Load full M5 regime history for XAUUSD, ordered.
	m5 := []M5State{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT ms.bar_time, COALESCE(ms.regime,'')
			FROM market_states ms
			JOIN symbols s ON s.id = ms.symbol_id
			WHERE s.symbol='XAUUSD' AND ms.period='M5'
			ORDER BY ms.bar_time ASC`)
		must(err)
		for rows.Next() {
			var st M5State
			must(rows.Scan(&st.BarTime, &st.Regime))
			m5 = append(m5, st)
		}
		rows.Close()
	}
	fmt.Fprintf(os.Stderr, "loaded %d M5 states\n", len(m5))

	// Load trend_follow XAUUSD trades (last 30 days), with entry M5 bar_time via signals.checked_market_states.
	trades := []Trade{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT p.side, f.net_profit, p.open_price, p.max_favorable,
			       (msq.bar_time)
			FROM fills f
			JOIN positions p ON p.id = f.our_position_id
			JOIN symbols s ON s.id = f.symbol_id
			LEFT JOIN orders o ON o.id = p.our_order_id
			LEFT JOIN signals sig ON sig.id = o.signal_id
			LEFT JOIN market_states msq ON msq.id::text = (sig.checked_market_states->'M5'->>'id')
			WHERE f.event_type='close' AND sig.strategy='trend_follow' AND s.symbol='XAUUSD'
			  
			  AND p.max_favorable IS NOT NULL AND msq.bar_time IS NOT NULL`)
		must(err)
		for rows.Next() {
			var t Trade
			must(rows.Scan(&t.Side, &t.NetProfit, &t.OpenPrice, &t.MaxFav, &t.EntryBar))
			trades = append(trades, t)
		}
		rows.Close()
	}
	fmt.Fprintf(os.Stderr, "loaded %d trades\n", len(trades))

	// For each trade, find its entry bar in the m5 slice, then count consecutive
	// prior bars with the same regime (streak length = "how stale is this alignment").
	streakFor := func(entryBar time.Time) int {
		idx := sort.Search(len(m5), func(i int) bool { return !m5[i].BarTime.Before(entryBar) })
		if idx >= len(m5) || !m5[idx].BarTime.Equal(entryBar) {
			return -1 // not found
		}
		regime := m5[idx].Regime
		streak := 0
		for i := idx; i >= 0 && m5[i].Regime == regime; i-- {
			streak++
		}
		return streak
	}

	type bucket struct {
		n, wins int
		sumFav, sumPnL float64
	}
	exact := map[int]*bucket{}

	for _, t := range trades {
		streak := streakFor(t.EntryBar)
		if streak < 0 {
			continue
		}
		if streak > 12 {
			streak = 13 // overflow bucket, printed as "13+"
		}
		favMove := t.MaxFav - t.OpenPrice
		if t.Side == "SELL" {
			favMove = t.OpenPrice - t.MaxFav
		}
		b := exact[streak]
		if b == nil {
			b = &bucket{}
			exact[streak] = b
		}
		b.n++
		b.sumFav += favMove
		b.sumPnL += t.NetProfit
		if t.NetProfit > 0 {
			b.wins++
		}
	}

	fmt.Printf("%-14s %6s %10s %14s %12s\n", "streak_bars", "n", "win_rate", "avg_fav_move", "total_pnl")
	for streak := 1; streak <= 13; streak++ {
		b := exact[streak]
		label := fmt.Sprintf("%d", streak)
		if streak == 13 {
			label = "13+"
		}
		if b == nil || b.n == 0 {
			fmt.Printf("%-14s %6d\n", label, 0)
			continue
		}
		fmt.Printf("%-14s %6d %9.1f%% %14.2f %12.2f\n", label, b.n, 100*float64(b.wins)/float64(b.n), b.sumFav/float64(b.n), b.sumPnL)
	}

	// Cumulative view: "if we only take entries with streak <= N, what's the aggregate?"
	fmt.Println("\ncumulative (streak <= N):")
	fmt.Printf("%-14s %6s %10s %12s\n", "streak<=N", "n", "win_rate", "total_pnl")
	for n := 1; n <= 13; n++ {
		var totalN, wins int
		var totalPnL float64
		for s := 1; s <= n; s++ {
			b := exact[s]
			if b == nil {
				continue
			}
			totalN += b.n
			wins += b.wins
			totalPnL += b.sumPnL
		}
		if totalN == 0 {
			continue
		}
		label := fmt.Sprintf("%d", n)
		if n == 13 {
			label = "13+ (all)"
		}
		fmt.Printf("%-14s %6d %9.1f%% %12.2f\n", label, totalN, 100*float64(wins)/float64(totalN), totalPnL)
	}
}
