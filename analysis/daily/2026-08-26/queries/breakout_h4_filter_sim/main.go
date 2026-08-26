package main

// Tests an H4-disagreement filter for breakout: pulls every real historical
// moment breakout's own Evaluate() actually signaled BUY/SELL (from
// signals table, both symbols — not just the ones that became live trades,
// since portfolio-level checks like CanOpen/cooldown/daily-loss-limit can
// block a real signal from becoming an order), classifies each by whether
// H4's regime agreed or disagreed with the trade direction, and forward-
// simulates outcome using real M1 candles and breakout's real SL/TP math
// (1.0x / 2.0x M15 ATR).
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

type Candle struct {
	BarTime         time.Time
	Open, High, Low float64
}

type Moment struct {
	Symbol    string
	SymbolID  string
	Side      string
	CreatedAt time.Time
	M15ATR    float64
	H4Regime  string
	EntryHint float64 // nearest M1 close at signal time, used as entry proxy
}

const slATRMult = 1.0
const tpATRMult = 2.0

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func simulate(sc []Candle, start, endTime time.Time, tp, sl float64, isBuy bool) int {
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
			return -1 // same-candle ambiguity: conservative, assume SL
		} else if tpHit {
			return 1
		} else if slHit {
			return -1
		}
	}
	return 0
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: breakout_h4_filter_sim <postgres-url>")
		os.Exit(1)
	}
	db, err := sql.Open("pgx", os.Args[1])
	must(err)
	defer db.Close()
	ctx := context.Background()

	moments := []Moment{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT s.symbol, sig.symbol_id::text, sig.signal, sig.created_at,
			       COALESCE(m15.atr,0), COALESCE(h4.regime,''),
			       COALESCE(m15.close,0)
			FROM signals sig
			JOIN symbols s ON s.id = sig.symbol_id
			LEFT JOIN market_states m15 ON m15.id::text = (sig.checked_market_states->'M15'->>'id')
			LEFT JOIN market_states h4 ON h4.id::text = (sig.checked_market_states->'H4'->>'id')
			WHERE sig.strategy='breakout' AND sig.signal IN ('BUY','SELL')`)
		must(err)
		for rows.Next() {
			var m Moment
			must(rows.Scan(&m.Symbol, &m.SymbolID, &m.Side, &m.CreatedAt, &m.M15ATR, &m.H4Regime, &m.EntryHint))
			moments = append(moments, m)
		}
		rows.Close()
	}
	fmt.Fprintf(os.Stderr, "loaded %d real breakout BUY/SELL signal moments\n", len(moments))

	candles := map[string][]Candle{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT symbol_id::text, bar_time, open, high, low
			FROM candles WHERE period='M1' ORDER BY symbol_id, bar_time ASC`)
		must(err)
		for rows.Next() {
			var sid string
			var c Candle
			must(rows.Scan(&sid, &c.BarTime, &c.Open, &c.High, &c.Low))
			candles[sid] = append(candles[sid], c)
		}
		rows.Close()
	}

	type bucket struct{ n, win, loss, unresolved int }
	buckets := map[string]*bucket{"agrees": {}, "disagrees": {}, "h4_ranging_or_breakout": {}, "no_h4": {}}

	for _, m := range moments {
		if m.M15ATR <= 0 || m.EntryHint == 0 {
			continue
		}
		isBuy := m.Side == "BUY"
		entry := m.EntryHint
		var sl, tp float64
		if isBuy {
			sl, tp = entry-slATRMult*m.M15ATR, entry+tpATRMult*m.M15ATR
		} else {
			sl, tp = entry+slATRMult*m.M15ATR, entry-tpATRMult*m.M15ATR
		}

		key := "h4_ranging_or_breakout"
		switch {
		case m.H4Regime == "":
			key = "no_h4"
		case (isBuy && m.H4Regime == "trending_up") || (!isBuy && m.H4Regime == "trending_down"):
			key = "agrees"
		case (isBuy && m.H4Regime == "trending_down") || (!isBuy && m.H4Regime == "trending_up"):
			key = "disagrees"
		}

		sc := candles[m.SymbolID]
		outcome := simulate(sc, m.CreatedAt, m.CreatedAt.Add(24*time.Hour), tp, sl, isBuy)
		b := buckets[key]
		b.n++
		switch outcome {
		case 1:
			b.win++
		case -1:
			b.loss++
		default:
			b.unresolved++
		}
	}

	fmt.Printf("%-24s %6s %6s %6s %10s %12s\n", "h4_relation", "n", "win", "loss", "unresolved", "win_rate")
	for _, key := range []string{"agrees", "disagrees", "h4_ranging_or_breakout", "no_h4"} {
		b := buckets[key]
		resolved := b.win + b.loss
		wr := 0.0
		if resolved > 0 {
			wr = 100 * float64(b.win) / float64(resolved)
		}
		fmt.Printf("%-24s %6d %6d %6d %10d %11.1f%%\n", key, b.n, b.win, b.loss, b.unresolved, wr)
	}
}
