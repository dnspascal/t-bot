// Full-history backtest of three non-price-action strategy candidates,
// same rigor and R:R construction as price_action_scan and structure_scan
// so all results are directly comparable:
//
//	vol_squeeze  — volatility_trend is computed and persisted per bar
//	  (internal/indicator/regime.go CalculateVolatilityTrend) but read by
//	  zero live strategies. Squeeze = 5 consecutive M15 bars NOT expanding,
//	  immediately followed by a bar that IS expanding and closes beyond the
//	  prior 10-bar M15 range (same window CalculateBreakoutLevel uses).
//
//	bollinger    — 20-bar SMA +/- 2 stdev on M15 closes. Nothing like this
//	  exists in internal/indicator at all (no stdev/Bollinger anywhere) —
//	  different math from RSI (price-distance-from-mean vs smoothed
//	  gain/loss ratio), so it's a genuinely different mean-reversion
//	  mechanism than rsi_reversal/dd_oversold_bounce/sr_bounce.
//
//	vol_climax   — volume and volume_ma are persisted per bar but only used
//	  as a minor secondary filter inside sr_bounce, never as a primary
//	  signal. Climax = M5 bar volume > 2x its volume_ma AND the bar makes a
//	  new 10-bar M5 high/low but closes back in the opposite half of its
//	  range (exhaustion) -> fade.
//
// All three use the same construction as the earlier tools: SL beyond the
// broken level/band + small ATR buffer, TP = 2x that risk, 24h resolution
// window, win rate on resolved trades only, R-expectancy. Each is also
// tested with the H1-trend-alignment filter for comparability.
//
// Run: DATABASE_URL=postgres://...@localhost:5433/tbot_prod go run main.go
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const winMin = 1440
const minATR = 1e-6
const slBufferATRFrac = 0.1
const rewardRiskRatio = 2.0
const swingLookback = 10
const squeezeBars = 5
const bollingerPeriod = 20
const bollingerK = 2.0

type Candle struct {
	BarTime         time.Time
	Open, High, Low float64
}

type M15Row struct {
	BarTime                time.Time
	Open, High, Low, Close float64
	ATR                    float64
	VolTrend               string
}

type M5Row struct {
	BarTime                time.Time
	Open, High, Low, Close float64
	ATR                    float64
	Volume, VolumeMA       float64
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

func detectVolSqueeze(m15 []M15Row) []signal {
	var out []signal
	for i := squeezeBars + swingLookback; i < len(m15); i++ {
		bar := m15[i]
		if bar.ATR < minATR || bar.VolTrend != "expanding" {
			continue
		}
		squeezed := true
		for k := 1; k <= squeezeBars; k++ {
			if m15[i-k].VolTrend == "expanding" {
				squeezed = false
				break
			}
		}
		if !squeezed {
			continue
		}
		rangeStart := i - swingLookback
		swingHigh := m15[rangeStart].High
		swingLow := m15[rangeStart].Low
		for _, p := range m15[rangeStart+1 : i] {
			if p.High > swingHigh {
				swingHigh = p.High
			}
			if p.Low < swingLow {
				swingLow = p.Low
			}
		}
		entry := bar.Close
		if bar.Close > swingHigh {
			sl := swingLow - slBufferATRFrac*bar.ATR
			risk := entry - sl
			if risk > 0 {
				out = append(out, signal{true, bar.BarTime, entry, sl, entry + rewardRiskRatio*risk})
			}
		} else if bar.Close < swingLow {
			sl := swingHigh + slBufferATRFrac*bar.ATR
			risk := sl - entry
			if risk > 0 {
				out = append(out, signal{false, bar.BarTime, entry, sl, entry - rewardRiskRatio*risk})
			}
		}
	}
	return out
}

func detectBollinger(m15 []M15Row) []signal {
	var out []signal
	for i := bollingerPeriod; i < len(m15); i++ {
		bar := m15[i]
		if bar.ATR < minATR {
			continue
		}
		window := m15[i-bollingerPeriod : i]
		sum := 0.0
		for _, w := range window {
			sum += w.Close
		}
		mean := sum / float64(len(window))
		sqSum := 0.0
		for _, w := range window {
			d := w.Close - mean
			sqSum += d * d
		}
		stdev := math.Sqrt(sqSum / float64(len(window)))
		if stdev <= 0 {
			continue
		}
		upper := mean + bollingerK*stdev
		lower := mean - bollingerK*stdev
		entry := bar.Close
		if bar.Close < lower {
			sl := bar.Low - slBufferATRFrac*bar.ATR
			risk := entry - sl
			if risk > 0 {
				out = append(out, signal{true, bar.BarTime, entry, sl, entry + rewardRiskRatio*risk})
			}
		} else if bar.Close > upper {
			sl := bar.High + slBufferATRFrac*bar.ATR
			risk := sl - entry
			if risk > 0 {
				out = append(out, signal{false, bar.BarTime, entry, sl, entry - rewardRiskRatio*risk})
			}
		}
	}
	return out
}

func detectVolClimax(m5 []M5Row) []signal {
	var out []signal
	for i := swingLookback; i < len(m5); i++ {
		bar := m5[i]
		if bar.ATR < minATR || bar.VolumeMA <= 0 || bar.Volume < 2*bar.VolumeMA {
			continue
		}
		rangeStart := i - swingLookback
		priorHigh := m5[rangeStart].High
		priorLow := m5[rangeStart].Low
		for _, p := range m5[rangeStart+1 : i] {
			if p.High > priorHigh {
				priorHigh = p.High
			}
			if p.Low < priorLow {
				priorLow = p.Low
			}
		}
		mid := (bar.High + bar.Low) / 2
		entry := bar.Close
		// climax top: new high made, but closes in bottom half -> exhaustion fade short
		if bar.High > priorHigh && bar.Close < mid {
			sl := bar.High + slBufferATRFrac*bar.ATR
			risk := sl - entry
			if risk > 0 {
				out = append(out, signal{false, bar.BarTime, entry, sl, entry - rewardRiskRatio*risk})
			}
		} else if bar.Low < priorLow && bar.Close > mid {
			sl := bar.Low - slBufferATRFrac*bar.ATR
			risk := entry - sl
			if risk > 0 {
				out = append(out, signal{true, bar.BarTime, entry, sl, entry + rewardRiskRatio*risk})
			}
		}
	}
	return out
}

func riskStats(signals []signal) (minR, p25, median, p75, maxR float64) {
	if len(signals) == 0 {
		return
	}
	risks := make([]float64, len(signals))
	for i, s := range signals {
		risks[i] = math.Abs(s.entry - s.sl)
	}
	sort.Float64s(risks)
	pct := func(p float64) float64 {
		idx := int(p * float64(len(risks)-1))
		return risks[idx]
	}
	return risks[0], pct(0.25), pct(0.5), pct(0.75), risks[len(risks)-1]
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: other_strategies_scan <postgres-url>")
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

		logf("%s: loading M5/M15/H1 market_states...", sym)
		var m5 []M5Row
		var m15 []M15Row
		var h1 []H1Row
		{
			rows, err := pool.Query(ctx, `
				SELECT bar_time, COALESCE(open,0), COALESCE(high,0), COALESCE(low,0), COALESCE(close,0), COALESCE(atr,0), COALESCE(volume,0), COALESCE(volume_ma,0)
				FROM market_states WHERE symbol_id=$1 AND period='M5' AND provider='ctrader' ORDER BY bar_time ASC`, symbolID)
			must(err)
			for rows.Next() {
				var r M5Row
				must(rows.Scan(&r.BarTime, &r.Open, &r.High, &r.Low, &r.Close, &r.ATR, &r.Volume, &r.VolumeMA))
				m5 = append(m5, r)
			}
			rows.Close()

			rows, err = pool.Query(ctx, `
				SELECT bar_time, COALESCE(open,0), COALESCE(high,0), COALESCE(low,0), COALESCE(close,0), COALESCE(atr,0), COALESCE(volatility_trend,'')
				FROM market_states WHERE symbol_id=$1 AND period='M15' AND provider='ctrader' ORDER BY bar_time ASC`, symbolID)
			must(err)
			for rows.Next() {
				var r M15Row
				must(rows.Scan(&r.BarTime, &r.Open, &r.High, &r.Low, &r.Close, &r.ATR, &r.VolTrend))
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
		logf("%s: %d M1 candles, %d M5 bars, %d M15 bars, %d H1 bars", sym, len(candles), len(m5), len(m15), len(h1))

		squeezeSignals := detectVolSqueeze(m15)
		bollingerSignals := detectBollinger(m15)
		climaxSignals := detectVolClimax(m5)
		logf("%s: %d squeeze signals, %d bollinger signals, %d climax signals", sym, len(squeezeSignals), len(bollingerSignals), len(climaxSignals))

		print := func(name string, r result) {
			fmt.Printf("%-20s matched=%-5d win=%-4d loss=%-4d unresolved=%-4d win_rate=%.1f%%  expectancy=%.2fR\n",
				name, r.matched, r.win, r.loss, r.unresolved, r.winRate(), r.expectancyR())
		}
		fmt.Printf("\n========== %s ==========\n", sym)
		print("vol_squeeze", aggregate(squeezeSignals, false, h1, candles))
		print("vol_squeeze+H1", aggregate(squeezeSignals, true, h1, candles))
		print("bollinger", aggregate(bollingerSignals, false, h1, candles))
		print("bollinger+H1", aggregate(bollingerSignals, true, h1, candles))
		print("vol_climax", aggregate(climaxSignals, false, h1, candles))
		print("vol_climax+H1", aggregate(climaxSignals, true, h1, candles))

		minR, p25, median, p75, maxR := riskStats(bollingerSignals)
		if pipSize > 0 {
			fmt.Printf("bollinger SL distance (pips): min=%.1f p25=%.1f median=%.1f p75=%.1f max=%.1f\n",
				minR/pipSize, p25/pipSize, median/pipSize, p75/pipSize, maxR/pipSize)
		} else {
			fmt.Printf("bollinger SL distance (price units): min=%.3f p25=%.3f median=%.3f p75=%.3f max=%.3f\n",
				minR, p25, median, p75, maxR)
		}
	}
}

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
