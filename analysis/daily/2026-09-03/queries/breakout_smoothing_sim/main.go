package main

// CalculateRegime's "breakout" branch is a single-bar check (this bar made
// a new 20-bar high/low, full stop) that overrides the otherwise-smoothed
// EMA(9)/EMA(21) trending_up/trending_down/ranging read. During a long
// sustained rally it re-fires every bar that prints a fresh high, for
// hours, which is exactly what happened before today's three losing
// trend_follow trades (H1 had been trending_up/breakout, alternating,
// since 01:00 -- never a fresh signal by 16:10).
//
// This tests a persistence rule: only call it "breakout" if price WASN'T
// already outside the same 20-bar range K bars ago. If it was already out
// there, it's not fresh anymore -- fall through to the EMA-based read
// instead of relabeling "breakout" again. Recomputes H1 regime from raw
// H1 candles (needed since the persisted regime column used the OLD,
// unsmoothed rule) for K in {2,3,5,8}, then replays trend_follow's real
// current entry gate (same methodology as trend_follow_exhaustion_sim)
// against each recomputed regime series, comparing to the current baseline.
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

const (
	maxFreshM5RegimeStreak      = 2
	minTrackedBarsBeforeTrustng = maxFreshM5RegimeStreak + 1
	slATRMult                   = 1.5
	tpATRMult                   = 2.5
	adxFloor                    = 20.0
	simWindowHrs                = 24
	dollarPerPt                 = 1.0
	refLookback                 = 20
)

type H1Candle struct {
	BarTime                time.Time
	Open, High, Low, Close float64
}
type H1Extra struct {
	EMAFast, EMASlow, ADX float64
}
type M5State struct {
	BarTime          time.Time
	Regime           string
	EMAFast, EMASlow float64
	Close            float64
}
type M15State struct {
	BarTime time.Time
	ATR     float64
}
type Candle struct {
	BarTime         time.Time
	Open, High, Low float64
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

// rawBreakoutDir returns "up"/"down"/"" for bar i under the ORIGINAL
// single-bar rule: does this bar's high/low exceed the range of the
// preceding refLookback bars (each bar judged against ITS OWN trailing
// window, not a shared/current one).
func rawBreakoutDir(h1c []H1Candle, i int) string {
	if i < refLookback {
		return ""
	}
	refHigh := h1c[i-refLookback].High
	refLow := h1c[i-refLookback].Low
	for j := i - refLookback + 1; j < i; j++ {
		if h1c[j].High > refHigh {
			refHigh = h1c[j].High
		}
		if h1c[j].Low < refLow {
			refLow = h1c[j].Low
		}
	}
	if h1c[i].High > refHigh {
		return "up"
	}
	if h1c[i].Low < refLow {
		return "down"
	}
	return ""
}

// recomputeRegime applies a K-bar grace period: "breakout" only counts
// while the CONSECUTIVE streak of same-direction raw breakout bars
// (ending at i) is <= k. Once a move has been setting new highs/lows for
// more than k bars in a row, it's no longer fresh -- fall through to the
// EMA-based trending/ranging read instead of relabeling "breakout" again.
func recomputeRegime(h1c []H1Candle, extra []H1Extra, k int) []string {
	raw := make([]string, len(h1c))
	for i := range h1c {
		raw[i] = rawBreakoutDir(h1c, i)
	}

	streak := make([]int, len(h1c))
	for i := range h1c {
		if raw[i] == "" {
			streak[i] = 0
			continue
		}
		if i > 0 && raw[i-1] == raw[i] {
			streak[i] = streak[i-1] + 1
		} else {
			streak[i] = 1
		}
	}

	regimes := make([]string, len(h1c))
	for i := range h1c {
		if raw[i] != "" && streak[i] <= k {
			regimes[i] = "breakout"
			continue
		}

		ef, es := extra[i].EMAFast, extra[i].EMASlow
		if ef == 0 || es == 0 {
			regimes[i] = "ranging"
			continue
		}
		gap := (ef - es) / ((ef + es) / 2)
		if gap < 0 {
			gap = -gap
		}
		switch {
		case gap < 0.001:
			regimes[i] = "ranging"
		case ef > es:
			regimes[i] = "trending_up"
		default:
			regimes[i] = "trending_down"
		}
	}
	return regimes
}

type result struct {
	name                     string
	n, win, lose, unresolved int
	pnl                      float64
}

func (r *result) record(outcome int, entry, exitPrice float64) {
	r.n++
	switch outcome {
	case 1:
		r.win++
	case -1:
		r.lose++
	default:
		r.unresolved++
	}
	r.pnl += (exitPrice - entry) * dollarPerPt
}
func (r result) print() {
	wr := 0.0
	if r.win+r.lose > 0 {
		wr = 100 * float64(r.win) / float64(r.win+r.lose)
	}
	fmt.Printf("%-28s n:%-5d win:%-4d lose:%-4d unresolved:%-4d winrate:%5.1f%%  pnl:%9.2f\n",
		r.name, r.n, r.win, r.lose, r.unresolved, wr, r.pnl)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: breakout_smoothing_sim <postgres-url>")
		os.Exit(1)
	}
	db, err := sql.Open("pgx", os.Args[1])
	must(err)
	defer db.Close()
	ctx := context.Background()

	h1c := []H1Candle{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT c.bar_time, c.open, c.high, c.low, c.close
			FROM candles c JOIN symbols s ON s.id = c.symbol_id
			WHERE s.symbol='XAUUSD' AND c.period='H1' ORDER BY c.bar_time ASC`)
		must(err)
		for rows.Next() {
			var c H1Candle
			must(rows.Scan(&c.BarTime, &c.Open, &c.High, &c.Low, &c.Close))
			h1c = append(h1c, c)
		}
		must(rows.Err())
		rows.Close()
	}

	extra := make([]H1Extra, len(h1c))
	adxByTime := map[int64]H1Extra{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT ms.bar_time, COALESCE(ms.ema_fast,0), COALESCE(ms.ema_slow,0), COALESCE(ms.adx,0)
			FROM market_states ms JOIN symbols s ON s.id = ms.symbol_id
			WHERE s.symbol='XAUUSD' AND ms.period='H1' ORDER BY ms.bar_time ASC`)
		must(err)
		for rows.Next() {
			var bt time.Time
			var e H1Extra
			must(rows.Scan(&bt, &e.EMAFast, &e.EMASlow, &e.ADX))
			adxByTime[bt.Unix()] = e
		}
		must(rows.Err())
		rows.Close()
	}
	for i, c := range h1c {
		extra[i] = adxByTime[c.BarTime.Unix()]
	}

	m5 := []M5State{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT ms.bar_time, COALESCE(ms.regime,''), COALESCE(ms.ema_fast,0), COALESCE(ms.ema_slow,0), COALESCE(ms.close,0)
			FROM market_states ms JOIN symbols s ON s.id = ms.symbol_id
			WHERE s.symbol='XAUUSD' AND ms.period='M5' ORDER BY ms.bar_time ASC`)
		must(err)
		for rows.Next() {
			var st M5State
			must(rows.Scan(&st.BarTime, &st.Regime, &st.EMAFast, &st.EMASlow, &st.Close))
			m5 = append(m5, st)
		}
		must(rows.Err())
		rows.Close()
	}

	m15 := []M15State{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT ms.bar_time, COALESCE(ms.atr,0)
			FROM market_states ms JOIN symbols s ON s.id = ms.symbol_id
			WHERE s.symbol='XAUUSD' AND ms.period='M15' ORDER BY ms.bar_time ASC`)
		must(err)
		for rows.Next() {
			var st M15State
			must(rows.Scan(&st.BarTime, &st.ATR))
			m15 = append(m15, st)
		}
		must(rows.Err())
		rows.Close()
	}

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
		must(rows.Err())
		rows.Close()
	}
	fmt.Fprintf(os.Stderr, "loaded %d H1 candles, %d M5, %d M15 states, %d M1 candles\n\n", len(h1c), len(m5), len(m15), len(candles))

	h1Times := make([]time.Time, len(h1c))
	for i, h := range h1c {
		h1Times[i] = h.BarTime
	}
	m15Times := make([]time.Time, len(m15))
	for i, m := range m15 {
		m15Times[i] = m.BarTime
	}
	nearestIdx := func(bt time.Time, times []time.Time) int {
		i := sort.Search(len(times), func(i int) bool { return times[i].After(bt) })
		return i - 1
	}
	candleIdxFor := func(bt time.Time) int {
		return sort.Search(len(candles), func(i int) bool { return !candles[i].BarTime.Before(bt) })
	}
	simulate := func(startIdx int, start time.Time, entryPx, sl, tp float64, isBuy bool) int {
		endTime := start.Add(simWindowHrs * time.Hour)
		for i := startIdx; i < len(candles); i++ {
			c := candles[i]
			if c.BarTime.Before(start) {
				continue
			}
			if c.BarTime.After(endTime) {
				break
			}
			var tpHit, slHit bool
			if isBuy {
				tpHit, slHit = c.High >= tp, c.Low <= sl
			} else {
				tpHit, slHit = c.Low <= tp, c.High >= sl
			}
			if slHit && tpHit {
				return -1
			} else if tpHit {
				return 1
			} else if slHit {
				return -1
			}
		}
		return 0
	}

	replay := func(label string, h1Regime []string) result {
		res := result{name: label}
		var lastM5Regime string
		var lastM5BarTime int64
		m5RegimeStreak := 0
		unseenBarsTracked := 0

		for _, bar := range m5 {
			barUnix := bar.BarTime.Unix()
			if barUnix != lastM5BarTime {
				if bar.Regime == lastM5Regime {
					m5RegimeStreak++
				} else {
					lastM5Regime = bar.Regime
					m5RegimeStreak = 1
				}
				lastM5BarTime = barUnix
				if unseenBarsTracked < minTrackedBarsBeforeTrustng {
					unseenBarsTracked++
				}
			}

			hi := nearestIdx(bar.BarTime, h1Times)
			if hi < refLookback || bar.BarTime.Sub(h1c[hi].BarTime) > 65*time.Minute {
				continue
			}
			regime := h1Regime[hi]
			adx := extra[hi].ADX
			if regime == "ranging" {
				continue
			}

			mi := nearestIdx(bar.BarTime, m15Times)
			if mi < 0 || bar.BarTime.Sub(m15[mi].BarTime) > 20*time.Minute || m15[mi].ATR <= 0 {
				continue
			}
			atr := m15[mi].ATR

			var dir string
			switch regime {
			case "trending_up":
				dir = "BUY"
			case "trending_down":
				dir = "SELL"
			case "breakout":
				if bar.EMAFast > bar.EMASlow {
					dir = "BUY"
				} else {
					dir = "SELL"
				}
			default:
				continue
			}

			isBuy := dir == "BUY"
			if isBuy && bar.EMAFast < bar.EMASlow {
				continue
			}
			if !isBuy && bar.EMAFast > bar.EMASlow {
				continue
			}
			if isBuy && bar.Regime != "trending_up" {
				continue
			}
			if !isBuy && bar.Regime != "trending_down" {
				continue
			}
			if unseenBarsTracked < minTrackedBarsBeforeTrustng {
				continue
			}
			if m5RegimeStreak > maxFreshM5RegimeStreak {
				continue
			}
			if adx < adxFloor {
				continue
			}

			entryPx := bar.Close
			var sl, tp float64
			if isBuy {
				sl, tp = entryPx-slATRMult*atr, entryPx+tpATRMult*atr
			} else {
				sl, tp = entryPx+slATRMult*atr, entryPx-tpATRMult*atr
			}
			idx := candleIdxFor(bar.BarTime)
			outcome := simulate(idx, bar.BarTime, entryPx, sl, tp, isBuy)
			exitPrice := entryPx
			if outcome == 1 {
				exitPrice = tp
			} else if outcome == -1 {
				exitPrice = sl
			}
			signedExit := exitPrice
			if !isBuy {
				signedExit = 2*entryPx - exitPrice
			}
			res.record(outcome, entryPx, signedExit)
		}
		return res
	}

	// Baseline: current production behavior (persisted regime column,
	// i.e. the unsmoothed single-bar breakout rule).
	baselineRegime := make([]string, len(h1c))
	{
		rows, err := db.QueryContext(ctx, `
			SELECT bar_time, COALESCE(regime,'') FROM market_states ms JOIN symbols s ON s.id=ms.symbol_id
			WHERE s.symbol='XAUUSD' AND ms.period='H1' ORDER BY bar_time ASC`)
		must(err)
		byTime := map[int64]string{}
		for rows.Next() {
			var bt time.Time
			var r string
			must(rows.Scan(&bt, &r))
			byTime[bt.Unix()] = r
		}
		must(rows.Err())
		rows.Close()
		for i, c := range h1c {
			baselineRegime[i] = byTime[c.BarTime.Unix()]
		}
	}

	baseline := replay("baseline (current, K=0/none)", baselineRegime)
	baseline.print()

	for _, k := range []int{1, 2, 3, 5, 8} {
		regimes := recomputeRegime(h1c, extra, k)
		r := replay(fmt.Sprintf("persistence K=%d bars", k), regimes)
		r.print()
	}
}
