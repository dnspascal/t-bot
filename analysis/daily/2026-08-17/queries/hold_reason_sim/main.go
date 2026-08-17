// Part 8 of analysis/daily/2026-08-17/report.html.
//
// Runs the REAL strategy code (not a re-implementation — imports the actual
// internal/strategy/* packages, same as cmd/bot) against every real M5 bar
// from today, for both symbols, using the live "all" roster (10 strategies).
// For each bar, reconstructs the exact states map each strategy reads and
// calls its real Evaluate(). Tallies: how many times each strategy actually
// signaled BUY/SELL vs held (and why), so "what's stopping every strategy"
// and "did we miss a real setup" can both be answered from real code
// behavior, not guesswork.
//
// Known gaps: sr_bounce runs without its ONNX ML filter (srbounce.New(nil, ...) —
// live production has the model loaded, so real sr_bounce is stricter than this
// simulation for that one strategy). session_open/session_momentum's optional
// SessionHigh/SessionLow/EMA50 checks read as unset (not persisted per-bar in
// market_states), so those specific soft-filters never engage here — a bar that
// would truly have session data available may show as blocked/unblocked
// differently live. Every other strategy's real entry logic runs exactly as
// deployed. currentPrice is approximated with the M5 candle close (consistent
// with every other tool in this investigation).
//
// This does NOT model portfolio-level constraints (max concurrent positions,
// max-per-strategy, same-direction spacing) — a "signal" here means the
// strategy's own entry gate said BUY/SELL, not that a real trade would
// necessarily have been taken once those additional limits are applied.
//
// Run: DATABASE_URL=postgres://...@localhost:5433/tbot_prod go run main.go
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
	"github.com/denismgaya/t-bot/internal/strategy/breakout"
	ddearlybreakout "github.com/denismgaya/t-bot/internal/strategy/dd_early_breakout"
	ddoversoldbounce "github.com/denismgaya/t-bot/internal/strategy/dd_oversold_bounce"
	ddrangingbreakout "github.com/denismgaya/t-bot/internal/strategy/dd_ranging_breakout"
	emapullback "github.com/denismgaya/t-bot/internal/strategy/ema_pullback"
	rsireversal "github.com/denismgaya/t-bot/internal/strategy/rsi_reversal"
	sessionmomentum "github.com/denismgaya/t-bot/internal/strategy/session_momentum"
	sessionopen "github.com/denismgaya/t-bot/internal/strategy/session_open"
	srbounce "github.com/denismgaya/t-bot/internal/strategy/sr_bounce"
	trendfollow "github.com/denismgaya/t-bot/internal/strategy/trend_follow"
)

type row struct {
	period  string
	barTime time.Time
	ms      indicator.MarketState
}

func newRoster() []strategy.Strategy {
	return []strategy.Strategy{
		srbounce.New(nil, 0, 0),
		breakout.New(),
		sessionopen.New(),
		trendfollow.New(),
		rsireversal.New(),
		sessionmomentum.New(),
		emapullback.New(),
		ddoversoldbounce.New(),
		ddrangingbreakout.New(),
		ddearlybreakout.New(),
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: hold_reason_sim <postgres-url>")
		os.Exit(1)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Args[1])
	must(err)
	defer pool.Close()

	dayStart, _ := time.Parse("2006-01-02 -0700", "2026-08-17 +0300")
	dayEnd := dayStart.AddDate(0, 0, 1)

	for _, sym := range []string{"XAUUSD", "EURUSD"} {
		var symbolID string
		var pipSize float64
		must(pool.QueryRow(ctx, `SELECT id FROM symbols WHERE symbol=$1`, sym).Scan(&symbolID))
		must(pool.QueryRow(ctx, `SELECT COALESCE(sc.pip_size,0) FROM symbol_configs sc WHERE sc.symbol_id=$1 AND sc.deleted_at IS NULL`, symbolID).Scan(&pipSize))

		byPeriod := map[string][]row{}
		rows, err := pool.Query(ctx, `
			SELECT period, bar_time, COALESCE(open,0), COALESCE(high,0), COALESCE(low,0), COALESCE(close,0),
			       COALESCE(ema_fast,0), COALESCE(ema_slow,0), COALESCE(rsi,0), COALESCE(adx,0), COALESCE(atr,0),
			       COALESCE(support_level,0), COALESCE(resistance_level,0), COALESCE(trend_high,0), COALESCE(trend_low,0),
			       COALESCE(breakout_level,0), COALESCE(regime,''), COALESCE(momentum_direction,'')
			FROM market_states
			WHERE symbol_id=$1 AND period IN ('M5','M15','M30','H1') AND provider='ctrader'
			  AND bar_time >= $2 AND bar_time < $3
			ORDER BY bar_time ASC`, symbolID, dayStart, dayEnd)
		must(err)
		for rows.Next() {
			var r row
			var ms indicator.MarketState
			must(rows.Scan(&r.period, &r.barTime, &ms.Open, &ms.High, &ms.Low, &ms.Close,
				&ms.EMAFast, &ms.EMASlow, &ms.RSI, &ms.ADX, &ms.ATR,
				&ms.SupportLevel, &ms.ResistanceLevel, &ms.TrendHigh, &ms.TrendLow,
				&ms.BreakoutLevel, &ms.Regime, &ms.MomentumDirection))
			ms.BarTime = r.barTime.Unix()
			ms.IsWarmedUp = true
			r.ms = ms
			byPeriod[r.period] = append(byPeriod[r.period], r)
		}
		rows.Close()

		latestAtOrBefore := func(period string, t time.Time) (indicator.MarketState, bool) {
			rs := byPeriod[period]
			idx := sort.Search(len(rs), func(i int) bool { return rs[i].barTime.After(t) }) - 1
			if idx < 0 {
				return indicator.MarketState{}, false
			}
			return rs[idx].ms, true
		}

		roster := newRoster()
		type tally struct {
			buy, sell int
			holds     map[string]int
		}
		results := map[string]*tally{}
		for _, s := range roster {
			results[s.Name()] = &tally{holds: map[string]int{}}
		}

		m5Bars := byPeriod[config.PeriodM5]
		for _, m5row := range m5Bars {
			states := map[string]indicator.MarketState{config.PeriodM5: m5row.ms}
			for _, p := range []string{config.PeriodM15, config.PeriodM30, config.PeriodH1} {
				if ms, ok := latestAtOrBefore(p, m5row.barTime); ok {
					states[p] = ms
				}
			}
			currentPrice := m5row.ms.Close

			for _, s := range roster {
				res := s.Evaluate(states, currentPrice, pipSize)
				t := results[s.Name()]
				switch res.Signal {
				case config.SignalBuy:
					t.buy++
				case config.SignalSell:
					t.sell++
				default:
					t.holds[res.Reason]++
				}
			}
		}

		fmt.Printf("\n========== %s (M5 bars evaluated: %d) ==========\n", sym, len(m5Bars))
		var names []string
		for n := range results {
			names = append(names, n)
		}
		sort.Strings(names)
		globalHolds := map[string]int{}
		for _, n := range names {
			t := results[n]
			fmt.Printf("  %-20s BUY=%-3d SELL=%-3d", n, t.buy, t.sell)
			type hr struct {
				reason string
				n      int
			}
			var top []hr
			for r, c := range t.holds {
				top = append(top, hr{r, c})
				globalHolds[fmt.Sprintf("[%s] %s", n, r)] += c
			}
			sort.Slice(top, func(i, j int) bool { return top[i].n > top[j].n })
			fmt.Println()
			for i := 0; i < len(top) && i < 5; i++ {
				fmt.Printf("      %-4d %s\n", top[i].n, top[i].reason)
			}
		}

		fmt.Println("  --- top 8 hold reasons across all strategies ---")
		var allHolds []struct {
			k string
			n int
		}
		for k, c := range globalHolds {
			allHolds = append(allHolds, struct {
				k string
				n int
			}{k, c})
		}
		sort.Slice(allHolds, func(i, j int) bool { return allHolds[i].n > allHolds[j].n })
		for i := 0; i < len(allHolds) && i < 8; i++ {
			fmt.Printf("    %-4d %s\n", allHolds[i].n, allHolds[i].k)
		}
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
