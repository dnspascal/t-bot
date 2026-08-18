// Checks whether the Stochastic-reversal candidate from
// standard_strategies_scan (+0.18R/+0.23R, the first positive-expectancy
// result of the day) is genuinely additive to the bot, or just a re-skin of
// rsi_reversal's existing thesis ("oscillator extreme -> reversal trade").
//
// Runs the REAL rsi_reversal package (not a re-implementation, same
// approach as hold_reason_sim) against every real M5 bar in full history.
// Cooldown/fail-streak state is NOT modeled (same gap as hold_reason_sim —
// OnClosed is never called since we don't have real trade outcomes to feed
// it, and fabricating them would bias the state machine); this means real
// rsi_reversal fires at least as often here as it would live, never less —
// a fair comparison for an overlap/redundancy check. Separately recomputes
// the Stochastic M15
// signals (same 14/3 slow-stochastic logic as standard_strategies_scan).
// Then measures overlap: for each Stochastic signal, is there a same-
// direction rsi_reversal signal within +/-15min? And the reverse: how much
// of rsi_reversal's own signal set is "covered" by a nearby Stochastic
// signal?
//
// Known gap (same as hold_reason_sim): market_states doesn't persist
// ema50/ema200, so rsi_reversal's `if h1.EMA50 > 0` price-vs-EMA50 gate
// never engages here — it's a strictly looser rsi_reversal than live.
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
	rsireversal "github.com/denismgaya/t-bot/internal/strategy/rsi_reversal"
)

type M5Row struct {
	BarTime                time.Time
	Open, High, Low, Close float64
}

type M15Row struct {
	BarTime                time.Time
	Open, High, Low, Close float64
	ATR                    float64
}

type H1Row struct {
	BarTime          time.Time
	RSI, ADX         float64
	EMAFast, EMASlow float64
	Regime           string
}

type sig struct {
	t   time.Time
	dir string
}

func atLatest[T any](rows []T, t time.Time, barTime func(T) time.Time) (T, bool) {
	idx := sort.Search(len(rows), func(i int) bool { return barTime(rows[i]).After(t) }) - 1
	if idx < 0 {
		var zero T
		return zero, false
	}
	return rows[idx], true
}

func detectStochastic(m15 []M15Row) []sig {
	const kPeriod = 14
	const dPeriod = 3
	var out []sig
	kVals := make([]float64, len(m15))
	for i := range m15 {
		if i < kPeriod-1 {
			kVals[i] = 50
			continue
		}
		window := m15[i-kPeriod+1 : i+1]
		hh, ll := window[0].High, window[0].Low
		for _, w := range window {
			if w.High > hh {
				hh = w.High
			}
			if w.Low < ll {
				ll = w.Low
			}
		}
		if hh-ll <= 0 {
			kVals[i] = 50
			continue
		}
		kVals[i] = 100 * (m15[i].Close - ll) / (hh - ll)
	}
	dVals := make([]float64, len(m15))
	for i := range m15 {
		if i < kPeriod-1+dPeriod-1 {
			dVals[i] = 50
			continue
		}
		sum := 0.0
		for k := 0; k < dPeriod; k++ {
			sum += kVals[i-k]
		}
		dVals[i] = sum / float64(dPeriod)
	}
	for i := kPeriod + dPeriod; i < len(m15); i++ {
		prevD, curD := dVals[i-1], dVals[i]
		if prevD < 20 && curD >= 20 {
			out = append(out, sig{m15[i].BarTime, config.SignalBuy})
		} else if prevD > 80 && curD <= 80 {
			out = append(out, sig{m15[i].BarTime, config.SignalSell})
		}
	}
	return out
}

func overlapRate(a, b []sig, windowMin int) (covered int) {
	win := time.Duration(windowMin) * time.Minute
	for _, s := range a {
		for _, o := range b {
			if o.dir != s.dir {
				continue
			}
			d := o.t.Sub(s.t)
			if d < 0 {
				d = -d
			}
			if d <= win {
				covered++
				break
			}
		}
	}
	return covered
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: stochastic_overlap_check <postgres-url>")
		os.Exit(1)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Args[1])
	must(err)
	defer pool.Close()

	for _, sym := range []string{"XAUUSD", "EURUSD"} {
		var symbolID string
		must(pool.QueryRow(ctx, `SELECT id FROM symbols WHERE symbol=$1`, sym).Scan(&symbolID))
		var pipSize float64
		must(pool.QueryRow(ctx, `SELECT COALESCE(sc.pip_size,0) FROM symbol_configs sc WHERE sc.symbol_id=$1 AND sc.deleted_at IS NULL`, symbolID).Scan(&pipSize))

		logf("%s: loading M5/M15/H1 market_states...", sym)
		var m5 []M5Row
		var m15 []M15Row
		var h1 []H1Row
		{
			rows, err := pool.Query(ctx, `
				SELECT bar_time, COALESCE(open,0), COALESCE(high,0), COALESCE(low,0), COALESCE(close,0)
				FROM market_states WHERE symbol_id=$1 AND period='M5' AND provider='ctrader' ORDER BY bar_time ASC`, symbolID)
			must(err)
			for rows.Next() {
				var r M5Row
				must(rows.Scan(&r.BarTime, &r.Open, &r.High, &r.Low, &r.Close))
				m5 = append(m5, r)
			}
			rows.Close()

			rows, err = pool.Query(ctx, `
				SELECT bar_time, COALESCE(open,0), COALESCE(high,0), COALESCE(low,0), COALESCE(close,0), COALESCE(atr,0)
				FROM market_states WHERE symbol_id=$1 AND period='M15' AND provider='ctrader' ORDER BY bar_time ASC`, symbolID)
			must(err)
			for rows.Next() {
				var r M15Row
				must(rows.Scan(&r.BarTime, &r.Open, &r.High, &r.Low, &r.Close, &r.ATR))
				m15 = append(m15, r)
			}
			rows.Close()

			rows, err = pool.Query(ctx, `
				SELECT bar_time, COALESCE(rsi,0), COALESCE(adx,0), COALESCE(ema_fast,0), COALESCE(ema_slow,0), COALESCE(regime,'')
				FROM market_states WHERE symbol_id=$1 AND period='H1' AND provider='ctrader' ORDER BY bar_time ASC`, symbolID)
			must(err)
			for rows.Next() {
				var r H1Row
				must(rows.Scan(&r.BarTime, &r.RSI, &r.ADX, &r.EMAFast, &r.EMASlow, &r.Regime))
				h1 = append(h1, r)
			}
			rows.Close()
		}
		logf("%s: %d M5, %d M15, %d H1 bars", sym, len(m5), len(m15), len(h1))

		rr := rsireversal.New()
		var rsiSignals []sig
		holdReasons := map[string]int{}
		for _, bar := range m5 {
			m15r, ok15 := atLatest(m15, bar.BarTime, func(x M15Row) time.Time { return x.BarTime })
			h1r, okH1 := atLatest(h1, bar.BarTime, func(x H1Row) time.Time { return x.BarTime })
			if !ok15 || !okH1 {
				continue
			}
			states := map[string]indicator.MarketState{
				config.PeriodM5: {
					Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close,
					IsWarmedUp: true, BarTime: bar.BarTime.Unix(),
				},
				config.PeriodM15: {
					ATR: m15r.ATR, IsWarmedUp: m15r.ATR > 0, BarTime: m15r.BarTime.Unix(),
				},
				config.PeriodH1: {
					RSI: h1r.RSI, ADX: h1r.ADX, EMAFast: h1r.EMAFast, EMASlow: h1r.EMASlow,
					Regime: h1r.Regime, IsWarmedUp: true, BarTime: h1r.BarTime.Unix(),
				},
			}
			res := rr.Evaluate(states, bar.Close, pipSize)
			if res.Signal == config.SignalBuy || res.Signal == config.SignalSell {
				rsiSignals = append(rsiSignals, sig{bar.BarTime, res.Signal})
			} else {
				holdReasons[res.Reason]++
			}
		}
		type hr struct {
			reason string
			n      int
		}
		var top []hr
		for r, c := range holdReasons {
			top = append(top, hr{r, c})
		}
		sort.Slice(top, func(i, j int) bool { return top[i].n > top[j].n })
		fmt.Printf("\n%s rsi_reversal hold reasons:\n", sym)
		for i := 0; i < len(top) && i < 8; i++ {
			fmt.Printf("  %-6d %s\n", top[i].n, top[i].reason)
		}

		stochSignals := detectStochastic(m15)

		coveredStoch := overlapRate(stochSignals, rsiSignals, 15)
		coveredRSI := overlapRate(rsiSignals, stochSignals, 15)

		fmt.Printf("\n========== %s ==========\n", sym)
		fmt.Printf("rsi_reversal real signals: %d\n", len(rsiSignals))
		fmt.Printf("stochastic signals:        %d\n", len(stochSignals))
		fmt.Printf("stochastic signals with a same-direction rsi_reversal signal within +/-15min: %d/%d (%.1f%%)\n",
			coveredStoch, len(stochSignals), pct(coveredStoch, len(stochSignals)))
		fmt.Printf("rsi_reversal signals with a same-direction stochastic signal within +/-15min:  %d/%d (%.1f%%)\n",
			coveredRSI, len(rsiSignals), pct(coveredRSI, len(rsiSignals)))
	}
}

func pct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(n) / float64(total)
}

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
