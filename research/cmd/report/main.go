// Weekly strategy report from the prod database (read-only): account, live and shadow trades per strategy in R,
// compared with the 10-year backtests, and what the strategies signalled.
// Usage: go run ./research/cmd/report -url-file <file with DATABASE_URL> [-days 7] [-out report.md]
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Backtest references, gold 2016-2026 at $0.20/oz cost with the live exit settings and limits.
var expected = map[string]struct {
	perWeek, avgR, win float64
	source             string
}{
	"month_start":          {0.66, 0.235, 29, "research/studies/2026-11-24-gold-candidates"},
	"trend_pullback":       {0.28, 0.101, 49, "research/studies/2026-11-24-gold-10y-grid"},
	"trend_pullback_nogap": {0.32, 0.076, 49, "research/studies/2026-11-24-gold-10y-grid"},
	"trend_follow":         {5.52, -0.010, 48, "research/studies/2026-11-24-gold-10y-grid"},
	"reopen_hour":          {4.85, -0.022, 46, "research/studies/2026-11-24-gold-holding"},
	"session_open":         {1.01, -0.054, 48, "research/studies/2026-11-24-gold-10y-grid"},
}

type trade struct {
	mode, strategy, side, reason string
	entry, sl                    float64
	opened                       time.Time
	closed                       sql.NullTime
	perOz, usd                   sql.NullFloat64
}

type agg struct {
	n, closed, wins  int
	usd, sumR, sumR2 float64
	reasons          map[string]int
}

func main() {
	urlFile := flag.String("url-file", "", "file holding the database URL (never printed)")
	days := flag.Int("days", 7, "report period in days, ending now")
	out := flag.String("out", "", "write the report here instead of stdout")
	flag.Parse()
	if *urlFile == "" {
		fail(fmt.Errorf("-url-file is required"))
	}
	raw, err := os.ReadFile(*urlFile)
	fail(err)
	db, err := sql.Open("pgx", strings.TrimSpace(string(raw)))
	fail(err)
	defer db.Close()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	fail(err)
	defer tx.Rollback()

	to := time.Now().UTC()
	from := to.AddDate(0, 0, -*days)
	var w io.Writer = os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		fail(err)
		defer f.Close()
		w = f
	}
	eat := time.FixedZone("EAT", 3*3600)
	p := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }

	var gold string
	fail(tx.QueryRowContext(ctx, `SELECT id FROM symbols WHERE symbol = 'XAUUSD'`).Scan(&gold))

	p("# Weekly strategy report\n\n%s to %s (EAT), gold only.\n\n", from.In(eat).Format("2006-01-02 15:04"), to.In(eat).Format("2006-01-02 15:04"))

	var startBal, endBal sql.NullFloat64
	_ = tx.QueryRowContext(ctx, `SELECT balance FROM account_snapshots WHERE snapshotted_at < $1 ORDER BY snapshotted_at DESC LIMIT 1`, from).Scan(&startBal)
	if !startBal.Valid {
		_ = tx.QueryRowContext(ctx, `SELECT balance FROM account_snapshots WHERE snapshotted_at >= $1 ORDER BY snapshotted_at LIMIT 1`, from).Scan(&startBal)
	}
	_ = tx.QueryRowContext(ctx, `SELECT balance FROM account_snapshots ORDER BY snapshotted_at DESC LIMIT 1`).Scan(&endBal)
	p("## Account\n\n| start balance | latest balance | change |\n|---|---|---|\n")
	if startBal.Valid && endBal.Valid {
		p("| $%.2f | $%.2f | $%+.2f |\n\n", startBal.Float64, endBal.Float64, endBal.Float64-startBal.Float64)
	} else {
		p("| n/a | n/a | n/a |\n\n")
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT mode, coalesce(strategy, '?'), side, coalesce(close_reason, ''), entry_price::float8, coalesce(sl, 0)::float8,
		       opened_at, closed_at, pnl_per_oz::float8, pnl_usd::float8
		FROM all_trades WHERE symbol_id = $1 AND opened_at >= $2 AND opened_at < $3 ORDER BY opened_at`, gold, from, to)
	fail(err)
	var trades []trade
	for rows.Next() {
		var t trade
		fail(rows.Scan(&t.mode, &t.strategy, &t.side, &t.reason, &t.entry, &t.sl, &t.opened, &t.closed, &t.perOz, &t.usd))
		trades = append(trades, t)
	}
	fail(rows.Err())
	rows.Close()

	stats := map[string]*agg{}
	for _, t := range trades {
		k := t.mode + "|" + t.strategy
		a := stats[k]
		if a == nil {
			a = &agg{reasons: map[string]int{}}
			stats[k] = a
		}
		a.n++
		if !t.closed.Valid || !t.perOz.Valid {
			continue
		}
		a.closed++
		if t.usd.Valid {
			a.usd += t.usd.Float64
		}
		if t.perOz.Float64 > 0 {
			a.wins++
		}
		if risk := math.Abs(t.entry - t.sl); risk > 0 {
			r := t.perOz.Float64 / risk
			a.sumR += r
			a.sumR2 += r * r
		}
		a.reasons[shortReason(t.reason)]++
	}

	weeks := float64(*days) / 7
	p("## Live strategies vs their 10-year backtests\n\n")
	p("| strategy | trades | closed | win %% | net $ | avg R | expected avg R | expected trades | verdict |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, name := range sortedKeys(stats, "live") {
		a := stats["live|"+name]
		e, ok := expected[name]
		avgR, win := 0.0, 0.0
		if a.closed > 0 {
			avgR, win = a.sumR/float64(a.closed), 100*float64(a.wins)/float64(a.closed)
		}
		exp, expN, verdict := "—", "—", "no backtest reference"
		if ok {
			exp, expN = fmt.Sprintf("%+.3f", e.avgR), fmt.Sprintf("%.1f", e.perWeek*weeks)
			verdict = judge(a, e.avgR)
		}
		p("| %s | %d | %d | %.0f | %+.2f | %+.3f | %s | %s | %s |\n", name, a.n, a.closed, win, a.usd, avgR, exp, expN, verdict)
	}
	for name, e := range expected {
		if stats["live|"+name] == nil {
			p("| %s | 0 | 0 | — | 0.00 | — | %+.3f | %.1f | no trades this period |\n", name, e.avgR, e.perWeek*weeks)
		}
	}
	p("\nR = profit per oz / initial stop distance. Verdict compares avg R with the backtest using a 2-standard-error band; it needs 10+ closed trades.\n\n")

	p("## Exits (live)\n\n| strategy | exits |\n|---|---|\n")
	for _, name := range sortedKeys(stats, "live") {
		p("| %s | %s |\n", name, reasons(stats["live|"+name].reasons))
	}

	p("\n## Shadow strategies (no real money, 1 oz)\n\n| strategy | trades | closed | win %% | net $ per oz | avg R |\n|---|---|---|---|---|---|\n")
	for _, name := range sortedKeys(stats, "shadow") {
		a := stats["shadow|"+name]
		avgR, win := 0.0, 0.0
		if a.closed > 0 {
			avgR, win = a.sumR/float64(a.closed), 100*float64(a.wins)/float64(a.closed)
		}
		p("| %s | %d | %d | %.0f | %+.2f | %+.3f |\n", name, a.n, a.closed, win, a.usd, avgR)
	}

	p("\n## Open live positions now\n\n")
	open := 0
	for _, t := range trades {
		if t.mode == "live" && !t.closed.Valid {
			open++
			p("- %s %s at %.2f, stop %.2f, opened %s EAT\n", t.strategy, t.side, t.entry, t.sl, t.opened.In(eat).Format("01-02 15:04"))
		}
	}
	if open == 0 {
		p("None.\n")
	}

	p("\n## Signals (non-HOLD) and what happened to them\n\n| strategy | signal | outcome | count |\n|---|---|---|---|\n")
	srows, err := tx.QueryContext(ctx, `
		SELECT strategy, signal, coalesce(nullif(skip_reason, ''), 'not skipped'), count(*)
		FROM signals WHERE symbol_id = $1 AND created_at >= $2 AND created_at < $3 AND signal <> 'HOLD'
		GROUP BY 1, 2, 3 ORDER BY 1, 4 DESC`, gold, from, to)
	fail(err)
	for srows.Next() {
		var s, sig, outcome string
		var n int
		fail(srows.Scan(&s, &sig, &outcome, &n))
		p("| %s | %s | %s | %d |\n", s, sig, outcome, n)
	}
	fail(srows.Err())
	srows.Close()
}

func judge(a *agg, want float64) string {
	if a.closed < 10 {
		return "too few trades to judge"
	}
	n := float64(a.closed)
	mean := a.sumR / n
	sd := math.Sqrt(math.Max(a.sumR2/n-mean*mean, 0))
	se := sd / math.Sqrt(n)
	switch {
	case mean < want-2*se:
		return "below backtest"
	case mean > want+2*se:
		return "above backtest"
	default:
		return "in line with backtest"
	}
}

func shortReason(r string) string {
	if r == "" {
		return "unknown"
	}
	switch {
	case strings.HasPrefix(r, "invalidation") || strings.Contains(r, "_against"):
		return "invalidation"
	case strings.HasPrefix(r, "peak_drawback"):
		return "peak_drawback"
	}
	return r
}

func reasons(m map[string]int) string {
	type kv struct {
		k string
		v int
	}
	var s []kv
	for k, v := range m {
		s = append(s, kv{k, v})
	}
	sort.Slice(s, func(i, j int) bool { return s[i].v > s[j].v })
	var parts []string
	for _, x := range s {
		parts = append(parts, fmt.Sprintf("%s %d", x.k, x.v))
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, ", ")
}

func sortedKeys(m map[string]*agg, mode string) []string {
	var out []string
	for k := range m {
		if strings.HasPrefix(k, mode+"|") {
			out = append(out, strings.TrimPrefix(k, mode+"|"))
		}
	}
	sort.Strings(out)
	return out
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
