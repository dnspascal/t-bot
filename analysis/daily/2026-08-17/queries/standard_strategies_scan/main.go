// Full-history backtest of three standard, well-established strategy types
// that don't exist anywhere in this codebase — none of the 10 live
// strategies use a Stochastic oscillator, MACD, or Ichimoku, and none of
// today's other tools (price_action_scan, structure_scan,
// other_strategies_scan) touched them either. Same rigor and comparability
// as everything else tested today.
//
// IMPORTANT construction fix vs. other_strategies_scan: that tool's
// Bollinger test looked spectacular (60% win rate, +0.8R) until a
// SL-distance check showed the median stop was ~1 pip on EURUSD — inside
// typical spread, a backtest artifact from deriving SL off the same
// extended bar's own tiny wick, not a real tradeable bracket. All three
// strategies here use a plain ATR-multiple SL/TP instead (SL = 1x M15 ATR,
// TP = 2x M15 ATR) — never derived from the signal bar's own range — so
// this mistake can't repeat.
//
//	stochastic — 14-period %K, 3-period %D (standard slow stochastic).
//	  %D crosses back above 20 from below -> BUY (oversold reversal). %D
//	  crosses back below 80 from above -> SELL. Different math from RSI
//	  (close's position within the recent high-low range, not a smoothed
//	  gain/loss ratio) even though both are "reversal oscillators."
//
//	macd_divergence — MACD(12,26,9) on M15 closes. Reuses the fractal
//	  swing-pivot detector built for structure_scan (5-bar fractal, N=2)
//	  to find confirmed price swing highs/lows, and compares the MACD
//	  line's value at consecutive same-type pivots: price makes a higher
//	  high but MACD makes a lower high at that same pivot -> bearish
//	  divergence -> SELL. Mirror for bullish divergence -> BUY.
//
//	ichimoku — Tenkan(9)/Kijun(26)/Senkou A&B(52), standard periods, cloud
//	  displaced forward 26 bars (Chikou-span confirmation omitted for
//	  tractability — noted, not silently skipped). Signal: Tenkan/Kijun
//	  cross confirmed by price closing outside the (displaced) cloud in
//	  the same direction.
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
)

const winMin = 1440
const minATR = 1e-6
const riskATRMult = 1.0
const rewardRiskRatio = 2.0
const fractalN = 2

type Candle struct {
	BarTime         time.Time
	Open, High, Low float64
}

type M15Row struct {
	BarTime                time.Time
	Open, High, Low, Close float64
	ATR                    float64
}

type H1Row struct {
	BarTime time.Time
	Regime  string
}

type signal struct {
	isBuy         bool
	entryTime     time.Time
	entry, sl, tp float64
}

func atLatest[T any](rows []T, t time.Time, barTime func(T) time.Time) (T, bool) {
	idx := sort.Search(len(rows), func(i int) bool { return barTime(rows[i]).After(t) }) - 1
	if idx < 0 {
		var zero T
		return zero, false
	}
	return rows[idx], true
}

func simulate(sc []Candle, start, endTime time.Time, entry, tp, sl float64, isBuy bool) int {
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
					return 1
				}
				return -1
			}
			if (entry - c.Low) >= (c.High - entry) {
				return 1
			}
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
	matched, win, loss, unresolved int
}

func (r result) winRate() float64 {
	total := r.win + r.loss
	if total == 0 {
		return 0
	}
	return 100 * float64(r.win) / float64(total)
}

func (r result) expectancyR() float64 {
	total := r.win + r.loss
	if total == 0 {
		return 0
	}
	return (rewardRiskRatio*float64(r.win) - float64(r.loss)) / float64(total)
}

func aggregate(signals []signal, requireH1Align bool, h1 []H1Row, candles []Candle) result {
	var r result
	for _, s := range signals {
		if requireH1Align {
			h1r, ok := atLatest(h1, s.entryTime, func(x H1Row) time.Time { return x.BarTime })
			if !ok {
				continue
			}
			if s.isBuy && h1r.Regime == "trending_down" {
				continue
			}
			if !s.isBuy && h1r.Regime == "trending_up" {
				continue
			}
		}
		r.matched++
		outcome := simulate(candles, s.entryTime, s.entryTime.Add(winMin*time.Minute), s.entry, s.tp, s.sl, s.isBuy)
		switch outcome {
		case 1:
			r.win++
		case -1:
			r.loss++
		default:
			r.unresolved++
		}
	}
	return r
}

func atrSignal(bar M15Row, isBuy bool, entryTime time.Time) (signal, bool) {
	if bar.ATR < minATR {
		return signal{}, false
	}
	entry := bar.Close
	risk := riskATRMult * bar.ATR
	if isBuy {
		return signal{true, entryTime, entry, entry - risk, entry + rewardRiskRatio*risk}, true
	}
	return signal{false, entryTime, entry, entry + risk, entry - rewardRiskRatio*risk}, true
}

// --- Stochastic ---

func detectStochastic(m15 []M15Row) []signal {
	const kPeriod = 14
	const dPeriod = 3
	var out []signal
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
		bar := m15[i]
		prevD, curD := dVals[i-1], dVals[i]
		if prevD < 20 && curD >= 20 {
			if s, ok := atrSignal(bar, true, bar.BarTime); ok {
				out = append(out, s)
			}
		} else if prevD > 80 && curD <= 80 {
			if s, ok := atrSignal(bar, false, bar.BarTime); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// --- MACD divergence ---

func emaSeries(values []float64, period int) []float64 {
	out := make([]float64, len(values))
	if len(values) == 0 {
		return out
	}
	k := 2.0 / (float64(period) + 1)
	out[0] = values[0]
	for i := 1; i < len(values); i++ {
		out[i] = values[i]*k + out[i-1]*(1-k)
	}
	return out
}

func detectMACDDivergence(m15 []M15Row) []signal {
	closes := make([]float64, len(m15))
	for i, b := range m15 {
		closes[i] = b.Close
	}
	ema12 := emaSeries(closes, 12)
	ema26 := emaSeries(closes, 26)
	macd := make([]float64, len(m15))
	for i := range m15 {
		macd[i] = ema12[i] - ema26[i]
	}

	type pivotEvt struct {
		isHigh      bool
		price       float64
		macdVal     float64
		confirmedAt time.Time
	}
	var events []pivotEvt
	for i := fractalN; i < len(m15)-fractalN; i++ {
		isHigh, isLow := true, true
		for k := 1; k <= fractalN; k++ {
			if m15[i-k].High > m15[i].High || m15[i+k].High > m15[i].High {
				isHigh = false
			}
			if m15[i-k].Low < m15[i].Low || m15[i+k].Low < m15[i].Low {
				isLow = false
			}
		}
		if isHigh {
			events = append(events, pivotEvt{true, m15[i].High, macd[i], m15[i+fractalN].BarTime})
		}
		if isLow {
			events = append(events, pivotEvt{false, m15[i].Low, macd[i], m15[i+fractalN].BarTime})
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].confirmedAt.Before(events[j].confirmedAt) })

	var out []signal
	var prevHighPrice, prevHighMACD float64
	var prevLowPrice, prevLowMACD float64
	haveHigh, haveLow := false, false
	barByTime := func(t time.Time) (M15Row, bool) {
		return atLatest(m15, t, func(x M15Row) time.Time { return x.BarTime })
	}

	for _, ev := range events {
		if ev.isHigh {
			if haveHigh && ev.price > prevHighPrice && ev.macdVal < prevHighMACD {
				if bar, ok := barByTime(ev.confirmedAt); ok {
					if s, ok := atrSignal(bar, false, ev.confirmedAt); ok {
						out = append(out, s)
					}
				}
			}
			prevHighPrice, prevHighMACD, haveHigh = ev.price, ev.macdVal, true
		} else {
			if haveLow && ev.price < prevLowPrice && ev.macdVal > prevLowMACD {
				if bar, ok := barByTime(ev.confirmedAt); ok {
					if s, ok := atrSignal(bar, true, ev.confirmedAt); ok {
						out = append(out, s)
					}
				}
			}
			prevLowPrice, prevLowMACD, haveLow = ev.price, ev.macdVal, true
		}
	}
	return out
}

// --- Ichimoku ---

func rollingMid(m15 []M15Row, i, period int) (float64, bool) {
	if i-period+1 < 0 {
		return 0, false
	}
	window := m15[i-period+1 : i+1]
	hh, ll := window[0].High, window[0].Low
	for _, w := range window {
		if w.High > hh {
			hh = w.High
		}
		if w.Low < ll {
			ll = w.Low
		}
	}
	return (hh + ll) / 2, true
}

func detectIchimoku(m15 []M15Row) []signal {
	const tenkanP, kijunP, spanBP, displacement = 9, 26, 52, 26
	n := len(m15)
	tenkan := make([]float64, n)
	kijun := make([]float64, n)
	spanA := make([]float64, n)
	spanB := make([]float64, n)
	ok := make([]bool, n)
	for i := 0; i < n; i++ {
		t, ok1 := rollingMid(m15, i, tenkanP)
		k, ok2 := rollingMid(m15, i, kijunP)
		b, ok3 := rollingMid(m15, i, spanBP)
		if ok1 && ok2 && ok3 {
			tenkan[i], kijun[i], spanB[i] = t, k, b
			spanA[i] = (t + k) / 2
			ok[i] = true
		}
	}

	var out []signal
	for i := displacement + 1; i < n; i++ {
		if !ok[i] || !ok[i-1] {
			continue
		}
		cloudIdx := i - displacement
		if cloudIdx < 0 || !ok[cloudIdx] {
			continue
		}
		cloudTop := spanA[cloudIdx]
		cloudBottom := spanB[cloudIdx]
		if cloudBottom > cloudTop {
			cloudTop, cloudBottom = cloudBottom, cloudTop
		}
		bar := m15[i]
		crossedUp := tenkan[i-1] <= kijun[i-1] && tenkan[i] > kijun[i]
		crossedDown := tenkan[i-1] >= kijun[i-1] && tenkan[i] < kijun[i]
		if crossedUp && bar.Close > cloudTop {
			if s, ok := atrSignal(bar, true, bar.BarTime); ok {
				out = append(out, s)
			}
		} else if crossedDown && bar.Close < cloudBottom {
			if s, ok := atrSignal(bar, false, bar.BarTime); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: standard_strategies_scan <postgres-url>")
		os.Exit(1)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Args[1])
	must(err)
	defer pool.Close()

	for _, sym := range []string{"XAUUSD", "EURUSD"} {
		var symbolID string
		must(pool.QueryRow(ctx, `SELECT id FROM symbols WHERE symbol=$1`, sym).Scan(&symbolID))

		logf("%s: loading M1 candles...", sym)
		var candles []Candle
		{
			rows, err := pool.Query(ctx, `SELECT bar_time, open, high, low FROM candles WHERE period='M1' AND symbol_id=$1 ORDER BY bar_time ASC`, symbolID)
			must(err)
			for rows.Next() {
				var c Candle
				must(rows.Scan(&c.BarTime, &c.Open, &c.High, &c.Low))
				candles = append(candles, c)
			}
			rows.Close()
		}

		logf("%s: loading M15/H1 market_states...", sym)
		var m15 []M15Row
		var h1 []H1Row
		{
			rows, err := pool.Query(ctx, `
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
				SELECT bar_time, COALESCE(regime,'')
				FROM market_states WHERE symbol_id=$1 AND period='H1' AND provider='ctrader' ORDER BY bar_time ASC`, symbolID)
			must(err)
			for rows.Next() {
				var r H1Row
				must(rows.Scan(&r.BarTime, &r.Regime))
				h1 = append(h1, r)
			}
			rows.Close()
		}
		logf("%s: %d M1 candles, %d M15 bars, %d H1 bars", sym, len(candles), len(m15), len(h1))

		stoch := detectStochastic(m15)
		macdDiv := detectMACDDivergence(m15)
		ichimoku := detectIchimoku(m15)
		logf("%s: %d stochastic signals, %d macd_divergence signals, %d ichimoku signals", sym, len(stoch), len(macdDiv), len(ichimoku))

		print := func(name string, r result) {
			fmt.Printf("%-20s matched=%-5d win=%-4d loss=%-4d unresolved=%-4d win_rate=%.1f%%  expectancy=%.2fR\n",
				name, r.matched, r.win, r.loss, r.unresolved, r.winRate(), r.expectancyR())
		}
		fmt.Printf("\n========== %s ==========\n", sym)
		print("stochastic", aggregate(stoch, false, h1, candles))
		print("stochastic+H1", aggregate(stoch, true, h1, candles))
		print("macd_divergence", aggregate(macdDiv, false, h1, candles))
		print("macd_divergence+H1", aggregate(macdDiv, true, h1, candles))
		print("ichimoku", aggregate(ichimoku, false, h1, candles))
		print("ichimoku+H1", aggregate(ichimoku, true, h1, candles))
	}
}

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
