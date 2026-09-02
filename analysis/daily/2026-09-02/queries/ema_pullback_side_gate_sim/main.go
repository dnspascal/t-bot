package main

// ema_pullback bounds price within a SYMMETRIC 1.0xATR band around H1's slow
// EMA — it never requires price to actually be on the pullback side (below
// the EMA for a SELL, above it for a BUY). Live evidence 2026-09-02: two
// SELL entries recovered fine (price genuinely below EMASlow, real bearish
// M5 candle, sane RSI); two SELL entries lost badly (one fired with price
// ALREADY $4.20 above EMASlow — broken through to the bullish side — RSI 77,
// confirmed only by a marginal $0.77 red candle inside a violent rally).
//
// This replays every historical moment XAUUSD's H1/ADX/M5 conditions matched
// the entry gate (not just the ones that live-fired) and compares:
//
//   current: today's actual logic (symmetric band, no side requirement)
//   sided:   same band, PLUS price must be on the correct side of EMASlow
//            (Buy: currentPrice >= EMASlow; Sell: currentPrice <= EMASlow)
//
// using the strategy's own real SL/TP math (SL=1.0x M15 ATR from EMASlow,
// TP=H1 TrendHigh/TrendLow) replayed against real M1 price data.
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
	BarTime               time.Time
	Open, Close, ATR, RSI float64
}
type H1State struct {
	BarTime             time.Time
	Regime              string
	EMASlow, ADX        float64
	TrendHigh, TrendLow float64
}
type M15State struct {
	BarTime time.Time
	ATR     float64
}
type Candle struct {
	BarTime         time.Time
	Open, High, Low float64
}

const (
	slATRMult        = 1.0
	emaProximityATRs = 1.0
	adxTrendFloor    = 25.0
	simWindowHrs     = 24
	dollarPerPt      = 1.0
)

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func simulate(candles []Candle, startIdx int, start time.Time, entry, sl, tp float64, isBuy bool) int {
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
	fmt.Printf("%-10s n:%-5d win:%-4d lose:%-4d unresolved:%-4d winrate:%5.1f%%  pnl:%9.2f\n",
		r.name, r.n, r.win, r.lose, r.unresolved, pct(r.win, r.win+r.lose), r.pnl)
}

func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return 100 * float64(n) / float64(d)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ema_pullback_side_gate_sim <postgres-url>")
		os.Exit(1)
	}
	db, err := sql.Open("pgx", os.Args[1])
	must(err)
	defer db.Close()
	ctx := context.Background()

	m5 := []M5State{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT ms.bar_time, COALESCE(ms.open,0), COALESCE(ms.close,0), COALESCE(ms.atr,0), COALESCE(ms.rsi,50)
			FROM market_states ms JOIN symbols s ON s.id = ms.symbol_id
			WHERE s.symbol='XAUUSD' AND ms.period='M5' ORDER BY ms.bar_time ASC`)
		must(err)
		for rows.Next() {
			var st M5State
			must(rows.Scan(&st.BarTime, &st.Open, &st.Close, &st.ATR, &st.RSI))
			m5 = append(m5, st)
		}
		rows.Close()
	}

	h1 := []H1State{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT ms.bar_time, COALESCE(ms.regime,''), COALESCE(ms.ema_slow,0), COALESCE(ms.adx,0),
			       COALESCE(ms.trend_high,0), COALESCE(ms.trend_low,0)
			FROM market_states ms JOIN symbols s ON s.id = ms.symbol_id
			WHERE s.symbol='XAUUSD' AND ms.period='H1' ORDER BY ms.bar_time ASC`)
		must(err)
		for rows.Next() {
			var st H1State
			must(rows.Scan(&st.BarTime, &st.Regime, &st.EMASlow, &st.ADX, &st.TrendHigh, &st.TrendLow))
			h1 = append(h1, st)
		}
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
	fmt.Fprintf(os.Stderr, "loaded %d M5, %d H1, %d M15 states, %d M1 candles\n", len(m5), len(h1), len(m15), len(candles))

	nearest := func(bt time.Time, times []time.Time) int {
		i := sort.Search(len(times), func(i int) bool { return times[i].After(bt) })
		return i - 1
	}
	h1Times := make([]time.Time, len(h1))
	for i, h := range h1 {
		h1Times[i] = h.BarTime
	}
	m15Times := make([]time.Time, len(m15))
	for i, m := range m15 {
		m15Times[i] = m.BarTime
	}
	candleIdxFor := func(bt time.Time) int {
		return sort.Search(len(candles), func(i int) bool { return !candles[i].BarTime.Before(bt) })
	}

	current := &result{name: "current"}
	sided := &result{name: "sided (hard)"}
	softSided := &result{name: "sided (0.3x overshoot ok)"}
	rsiFilter := &result{name: "rsi not extreme (<70/>30)"}
	rsiExcluded := &result{name: "  [excluded by RSI filter]"}
	combined := &result{name: "soft-side + rsi filter"}
	adxDecay4h5 := &result{name: "adx decline <=5pt over 4h"}
	adxDecay4h5Excl := &result{name: "  [excluded by 4h/5pt]"}
	adxDecay6h8 := &result{name: "adx decline <=8pt over 6h"}
	adxRsiCombo := &result{name: "adx decline<=8/6h + rsi filter"}
	sidedSkipped := 0

	// Threshold grid around the 4h/5pt result — checking it's a robust
	// signal, not a single lucky cutoff.
	grid := []struct {
		hrs        time.Duration
		maxDecline float64
	}{
		{3 * time.Hour, 3}, {3 * time.Hour, 5}, {3 * time.Hour, 7},
		{4 * time.Hour, 3}, {4 * time.Hour, 5}, {4 * time.Hour, 7}, {4 * time.Hour, 10},
		{5 * time.Hour, 5}, {5 * time.Hour, 7}, {5 * time.Hour, 10},
		{6 * time.Hour, 5}, {6 * time.Hour, 8}, {6 * time.Hour, 10},
	}
	gridResults := make([]*result, len(grid))
	gridExcluded := make([]*result, len(grid))
	for i, g := range grid {
		gridResults[i] = &result{name: fmt.Sprintf("  grid %v/<=%.0fpt", g.hrs, g.maxDecline)}
		gridExcluded[i] = &result{name: "    excluded"}
	}

	// 2026-09-02 13:35/13:45 +02:00 (EAT) — matched via UTC epoch since the DB
	// driver may return timestamptz as UTC regardless of session display tz.
	target1, _ := time.Parse(time.RFC3339, "2026-09-02T11:35:00Z")
	target2, _ := time.Parse(time.RFC3339, "2026-09-02T11:45:00Z")
	isTodayEntry := func(bt time.Time) bool {
		u := bt.UTC()
		return u.Equal(target1) || u.Equal(target2)
	}

	for _, bar := range m5 {
		if bar.ATR <= 0 {
			continue
		}
		hi := nearest(bar.BarTime, h1Times)
		if hi < 0 || bar.BarTime.Sub(h1[hi].BarTime) > 65*time.Minute {
			continue
		}
		h := h1[hi]
		if h.ADX < adxTrendFloor {
			continue
		}
		var dir string
		switch h.Regime {
		case "trending_up":
			dir = "BUY"
		case "trending_down":
			dir = "SELL"
		default:
			continue
		}

		currentPrice := bar.Close
		emaProximity := emaProximityATRs * bar.ATR
		distanceFromEMA := abs(currentPrice - h.EMASlow)
		if distanceFromEMA > emaProximity {
			continue
		}
		if dir == "BUY" && currentPrice > h.EMASlow+emaProximity {
			continue
		}
		if dir == "SELL" && currentPrice < h.EMASlow-emaProximity {
			continue
		}
		if dir == "BUY" && bar.Close <= bar.Open {
			continue
		}
		if dir == "SELL" && bar.Close >= bar.Open {
			continue
		}

		mi := nearest(bar.BarTime, m15Times)
		if mi < 0 || bar.BarTime.Sub(m15[mi].BarTime) > 20*time.Minute || m15[mi].ATR <= 0 {
			continue
		}
		atr := m15[mi].ATR

		var slPrice, tpPrice float64
		isBuy := dir == "BUY"
		if isBuy {
			slPrice = h.EMASlow - slATRMult*atr
			tpPrice = h.TrendHigh
		} else {
			slPrice = h.EMASlow + slATRMult*atr
			tpPrice = h.TrendLow
		}
		if isBuy && tpPrice <= currentPrice {
			continue
		}
		if !isBuy && tpPrice >= currentPrice {
			continue
		}
		slDist := abs(currentPrice - slPrice)
		tpDist := abs(tpPrice - currentPrice)
		if slDist < 0.03 || tpDist < 0.03 { // ~3 pips at 0.01 pipSize for gold, matches "<3" pips check loosely
			continue
		}
		if tpDist < slDist {
			continue
		}

		idx := candleIdxFor(bar.BarTime)
		outcome := simulate(candles, idx, bar.BarTime, currentPrice, slPrice, tpPrice, isBuy)

		// pnlPerPt: signed so (entry, signedExit) always yields the real $
		// P&L via (signedExit-entry)*dollarPerPt, for either direction.
		signedExit := currentPrice
		if outcome == 1 {
			signedExit = tpPrice
		} else if outcome == -1 {
			signedExit = slPrice
		}
		if !isBuy {
			signedExit = 2*currentPrice - signedExit // reflect: SELL profits when price falls
		}

		current.record(outcome, currentPrice, signedExit)

		// overshoot: how far price is past EMASlow on the WRONG side (0 or
		// negative means it's on the correct/pullback side).
		var overshoot float64
		if isBuy {
			overshoot = h.EMASlow - currentPrice // positive = price below EMA, wrong side for a BUY
		} else {
			overshoot = currentPrice - h.EMASlow // positive = price above EMA, wrong side for a SELL
		}
		onCorrectSide := overshoot <= 0
		softOK := overshoot <= 0.3*bar.ATR
		// Reject when momentum is extreme AGAINST the trade: a SELL fired
		// while RSI is deeply overbought (>70, strong bullish momentum), or
		// a BUY fired while RSI is deeply oversold (<30).
		rsiOK := (isBuy && bar.RSI >= 30) || (!isBuy && bar.RSI <= 70)

		if !onCorrectSide {
			sidedSkipped++
		} else {
			sided.record(outcome, currentPrice, signedExit)
		}
		if softOK {
			softSided.record(outcome, currentPrice, signedExit)
		}
		if rsiOK {
			rsiFilter.record(outcome, currentPrice, signedExit)
		} else {
			rsiExcluded.record(outcome, currentPrice, signedExit)
		}
		if softOK && rsiOK {
			combined.record(outcome, currentPrice, signedExit)
		}

		// ADX decay: how much has H1 ADX fallen over the trailing window —
		// a proxy for "is this trend actively fading," distinct from its
		// instantaneous level (which the adxTrendFloor check already covers).
		adx4hAgoIdx := nearest(bar.BarTime.Add(-4*time.Hour), h1Times)
		adx6hAgoIdx := nearest(bar.BarTime.Add(-6*time.Hour), h1Times)
		if adx4hAgoIdx >= 0 {
			decline4h := h1[adx4hAgoIdx].ADX - h.ADX
			if decline4h <= 5 {
				adxDecay4h5.record(outcome, currentPrice, signedExit)
			} else {
				adxDecay4h5Excl.record(outcome, currentPrice, signedExit)
			}
		}
		if adx6hAgoIdx >= 0 {
			decline6h := h1[adx6hAgoIdx].ADX - h.ADX
			adxOK := decline6h <= 8
			if adxOK {
				adxDecay6h8.record(outcome, currentPrice, signedExit)
			}
			if adxOK && rsiOK {
				adxRsiCombo.record(outcome, currentPrice, signedExit)
			}
		}

		for i, g := range grid {
			idx := nearest(bar.BarTime.Add(-g.hrs), h1Times)
			if idx < 0 {
				continue
			}
			decline := h1[idx].ADX - h.ADX
			passes := decline <= g.maxDecline
			if passes {
				gridResults[i].record(outcome, currentPrice, signedExit)
			} else {
				gridExcluded[i].record(outcome, currentPrice, signedExit)
			}
			if isTodayEntry(bar.BarTime) {
				fmt.Fprintf(os.Stderr, "  today's %s: grid %v/<=%.0fpt -> decline=%.2f, passes=%v (would %s)\n",
					bar.BarTime.Format("15:04"), g.hrs, g.maxDecline, decline, passes,
					map[bool]string{true: "STILL FIRE", false: "BE BLOCKED"}[passes])
			}
		}
	}

	fmt.Println()
	current.print()
	sided.print()
	softSided.print()
	rsiFilter.print()
	rsiExcluded.print()
	combined.print()
	adxDecay4h5.print()
	adxDecay4h5Excl.print()
	adxDecay6h8.print()
	adxRsiCombo.print()
	fmt.Println("\nthreshold grid (robustness check):")
	for i := range gridResults {
		gridResults[i].print()
		gridExcluded[i].print()
	}
	fmt.Printf("\nskipped by hard side requirement (price on wrong side of EMASlow at entry): %d\n", sidedSkipped)
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
