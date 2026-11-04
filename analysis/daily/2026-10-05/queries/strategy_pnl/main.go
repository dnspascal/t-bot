// XAUUSD per-strategy realized PnL from closing fills: dd_* vs hand-designed strategies.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	db, err := sql.Open("pgx", os.Args[1])
	must(err)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	must(err)
	defer tx.Rollback()

	run := func(title, q string) {
		fmt.Printf("\n== %s ==\n", title)
		fmt.Printf("%-22s %-8s %5s %6s %10s %9s %9s %7s %6s\n", "strategy", "symbol", "n", "win%", "net", "avgWin", "avgLoss", "PF", "exp")
		rows, err := tx.QueryContext(ctx, q)
		must(err)
		defer rows.Close()
		for rows.Next() {
			var strat, sym string
			var n, wins int
			var net, grossWin, grossLoss float64
			must(rows.Scan(&strat, &sym, &n, &wins, &net, &grossWin, &grossLoss))
			avgW, avgL, pf := 0.0, 0.0, 0.0
			if wins > 0 {
				avgW = grossWin / float64(wins)
			}
			if n-wins > 0 {
				avgL = grossLoss / float64(n-wins)
			}
			if grossLoss != 0 {
				pf = grossWin / -grossLoss
			}
			fmt.Printf("%-22s %-8s %5d %5.1f%% %10.2f %9.2f %9.2f %7.2f %6.2f\n",
				strat, sym, n, 100*float64(wins)/float64(n), net, avgW, avgL, pf, net/float64(n))
		}
		must(rows.Err())
	}

	base := `
		WITH closes AS (
			SELECT COALESCE(NULLIF(sig.strategy,''),'(none)') AS strat, s.symbol AS sym,
			       SUM(f.net_profit) AS pnl, MIN(f.provider_exec_time) AS t
			FROM fills f
			JOIN positions p ON p.id = f.our_position_id
			JOIN orders o ON o.id = p.our_order_id
			LEFT JOIN signals sig ON sig.id = o.signal_id
			JOIN symbols s ON s.id = p.symbol_id
			WHERE f.gross_profit IS NOT NULL AND f.closed_volume > 0 AND s.symbol = 'XAUUSD' %s
			GROUP BY p.id, 1, 2
		)
		SELECT strat, %s, COUNT(*), COUNT(*) FILTER (WHERE pnl > 0),
		       COALESCE(SUM(pnl),0), COALESCE(SUM(pnl) FILTER (WHERE pnl > 0),0), COALESCE(SUM(pnl) FILTER (WHERE pnl <= 0),0)
		FROM closes GROUP BY 1, 2 ORDER BY 1, 2`

	run("all time, by strategy", fmt.Sprintf(base, "", "'all'"))
	run("last 14 days, by strategy", fmt.Sprintf(base, "AND f.provider_exec_time >= now() - interval '14 days'", "'all'"))
	run("last 30 days, by strategy", fmt.Sprintf(base, "AND f.provider_exec_time >= now() - interval '30 days'", "'all'"))
	run("by family (dd_* vs rest), all time", `
		WITH closes AS (
			SELECT CASE WHEN sig.strategy LIKE 'dd\_%' THEN 'dd_*' ELSE 'designed' END AS strat,
			       SUM(f.net_profit) AS pnl
			FROM fills f
			JOIN positions p ON p.id = f.our_position_id
			JOIN orders o ON o.id = p.our_order_id
			LEFT JOIN signals sig ON sig.id = o.signal_id
			JOIN symbols s ON s.id = p.symbol_id
			WHERE f.gross_profit IS NOT NULL AND f.closed_volume > 0 AND s.symbol = 'XAUUSD'
			GROUP BY p.id, 1
		)
		SELECT strat, 'all', COUNT(*), COUNT(*) FILTER (WHERE pnl > 0),
		       SUM(pnl), COALESCE(SUM(pnl) FILTER (WHERE pnl > 0),0), COALESCE(SUM(pnl) FILTER (WHERE pnl <= 0),0)
		FROM closes GROUP BY 1 ORDER BY 1`)
}
