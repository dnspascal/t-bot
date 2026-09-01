package main

// dd_oversold_bounce enters BUY the moment its full condition group is met
// (H1 trending_down, M5 trending_down|breakout, M5 EMA bearish, M5 RSI<50,
// M5 ADX>=25) — all conditions that just mean "still actively falling," no
// confirmation the fall has stopped. Live evidence: today's two entries both
// saw price run well past the SL before reversing.
//
// This replays every historical moment the full condition group was met for
// XAUUSD and tests SEVERAL candidate confirmation gates against the naive
// (enter immediately) baseline, using the strategy's own real SL/TP math
// (SL=1.0xATR, TP=1.5xATR) replayed against real M1 price data. All gates
// scan forward up to 12 M5 bars (1h) from the same qualifying instance, so
// results are directly comparable to each other and to naive.
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
	BarTime             time.Time
	Regime, MomentumDir string
	Open, Close         float64
	Low                 float64
	EMAFast, EMASlow    float64
	RSI, ADX, ATR       float64
}
type H1State struct {
	BarTime time.Time
	Regime  string
}
type Candle struct {
	BarTime         time.Time
	Open, High, Low float64
}

const (
	adxTrending  = 25.0
	tpATRMult    = 1.5
	slATRMult    = 1.0
	dollarPerPt  = 1.0
	confirmBars  = 12
	simWindowHrs = 24
)

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func stillValid(m M5State) bool {
	if m.Regime != "trending_down" && m.Regime != "breakout" {
		return false
	}
	if m.EMAFast >= m.EMASlow || m.RSI >= 50 || m.ATR <= 0 {
		return false
	}
	return true
}

func qualifies(m M5State) bool {
	return stillValid(m) && m.ADX >= adxTrending
}

func simulate(candles []Candle, startIdx int, start time.Time, entry, sl, tp float64) int {
	endTime := start.Add(simWindowHrs * time.Hour)
	for i := startIdx; i < len(candles); i++ {
		c := candles[i]
		if c.BarTime.Before(start) {
			continue
		}
		if c.BarTime.After(endTime) {
			break
		}
		tpHit := c.High >= tp
		slHit := c.Low <= sl
		if tpHit && slHit {
			return -1
		} else if tpHit {
			return 1
		} else if slHit {
			return -1
		}
	}
	return 0
}

type result struct {
	name                  string
	found, givenUp        int
	win, lose, unresolved int
	pnl                   float64
}

func (r *result) record(outcome int, entry, exitPrice float64) {
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
	fmt.Printf("%-28s found:%-4d givenUp:%-4d win:%-4d lose:%-4d winrate:%5.1f%%  pnl:%8.2f\n",
		r.name, r.found, r.givenUp, r.win, r.lose, pct(r.win, r.win+r.lose), r.pnl)
}

func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return 100 * float64(n) / float64(d)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: oversold_bounce_confirmation_sim <postgres-url>")
		os.Exit(1)
	}
	db, err := sql.Open("pgx", os.Args[1])
	must(err)
	defer db.Close()
	ctx := context.Background()

	m5 := []M5State{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT ms.bar_time, COALESCE(ms.regime,''), COALESCE(ms.momentum_direction,''),
			       COALESCE(ms.open,0), COALESCE(ms.close,0), COALESCE(ms.low,0),
			       COALESCE(ms.ema_fast,0), COALESCE(ms.ema_slow,0),
			       COALESCE(ms.rsi,0), COALESCE(ms.adx,0), COALESCE(ms.atr,0)
			FROM market_states ms JOIN symbols s ON s.id = ms.symbol_id
			WHERE s.symbol='XAUUSD' AND ms.period='M5' ORDER BY ms.bar_time ASC`)
		must(err)
		for rows.Next() {
			var st M5State
			must(rows.Scan(&st.BarTime, &st.Regime, &st.MomentumDir, &st.Open, &st.Close, &st.Low,
				&st.EMAFast, &st.EMASlow, &st.RSI, &st.ADX, &st.ATR))
			m5 = append(m5, st)
		}
		rows.Close()
	}

	h1 := []H1State{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT ms.bar_time, COALESCE(ms.regime,'')
			FROM market_states ms JOIN symbols s ON s.id = ms.symbol_id
			WHERE s.symbol='XAUUSD' AND ms.period='H1' ORDER BY ms.bar_time ASC`)
		must(err)
		for rows.Next() {
			var st H1State
			must(rows.Scan(&st.BarTime, &st.Regime))
			h1 = append(h1, st)
		}
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
		rows.Close()
	}
	fmt.Fprintf(os.Stderr, "loaded %d M5 states, %d H1 states, %d M1 candles\n", len(m5), len(h1), len(candles))

	nearestH1 := func(bt time.Time) (H1State, bool) {
		var best H1State
		found := false
		for _, h := range h1 {
			if !h.BarTime.After(bt) && (!found || h.BarTime.After(best.BarTime)) {
				best, found = h, true
			}
		}
		if found && bt.Sub(best.BarTime) <= 65*time.Minute {
			return best, true
		}
		return H1State{}, false
	}
	candleIdxFor := func(bt time.Time) int {
		return sort.Search(len(candles), func(i int) bool { return !candles[i].BarTime.Before(bt) })
	}

	enterAt := func(r *result, bar M5State) {
		entry := bar.Close
		sl := entry - slATRMult*bar.ATR
		tp := entry + tpATRMult*bar.ATR
		idx := candleIdxFor(bar.BarTime)
		outcome := simulate(candles, idx, bar.BarTime, entry, sl, tp)
		exitPrice := entry
		if outcome == 1 {
			exitPrice = tp
		} else if outcome == -1 {
			exitPrice = sl
		}
		r.record(outcome, entry, exitPrice)
	}

	naive := &result{name: "naive (enter immediately)"}
	rsiUptick := &result{name: "RSI uptick (1 bar)"}
	rsiUptickX2 := &result{name: "RSI uptick x2 (consecutive)"}
	bullishClose := &result{name: "M5 bullish candle (close>open)"}
	higherLow := &result{name: "higher low (low>prev low)"}
	adxDeclining := &result{name: "ADX declining (momentum fading)"}
	momentumRising := &result{name: "momentum_direction=Rising"}

	checked := 0
	for i, bar := range m5 {
		h, ok := nearestH1(bar.BarTime)
		if !ok || h.Regime != "trending_down" {
			continue
		}
		if !qualifies(bar) {
			continue
		}
		checked++
		naive.found++
		enterAt(naive, bar)

		type gate struct {
			r     *result
			match func(prev, cur M5State, consecUp int) bool
		}
		gates := []gate{
			{rsiUptick, func(prev, cur M5State, _ int) bool { return cur.RSI > prev.RSI }},
			{rsiUptickX2, func(prev, cur M5State, consecUp int) bool { return consecUp >= 2 }},
			{bullishClose, func(prev, cur M5State, _ int) bool { return cur.Close > cur.Open }},
			{higherLow, func(prev, cur M5State, _ int) bool { return cur.Low > prev.Low }},
			{adxDeclining, func(prev, cur M5State, _ int) bool { return cur.ADX < prev.ADX }},
			{momentumRising, func(prev, cur M5State, _ int) bool { return cur.MomentumDir == "rising" }},
		}

		consecUp := 0
		matched := make([]bool, len(gates))
		for j := i + 1; j < len(m5) && j <= i+confirmBars; j++ {
			cur := m5[j]
			prev := m5[j-1]
			if !stillValid(cur) {
				break
			}
			if cur.RSI > prev.RSI {
				consecUp++
			} else {
				consecUp = 0
			}
			for gi, g := range gates {
				if matched[gi] {
					continue
				}
				if g.match(prev, cur, consecUp) {
					matched[gi] = true
					g.r.found++
					enterAt(g.r, cur)
				}
			}
		}
		for gi, g := range gates {
			if !matched[gi] {
				g.r.givenUp++
			}
		}
	}

	fmt.Printf("\nqualifying instances: %d\n\n", checked)
	naive.print()
	rsiUptick.print()
	rsiUptickX2.print()
	bullishClose.print()
	higherLow.print()
	adxDeclining.print()
	momentumRising.print()
}
