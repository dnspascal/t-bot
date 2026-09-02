package main

// Today's peak_drawback/breakeven design is two hard percentage-of-TP-distance
// gates: breakeven arms at 50% (protects only the entry price, none of the
// gain), peak_drawback only engages once 70% is reached (protects 40% of
// TOTAL tp distance from there). Between those, and below 50%, nothing
// protects any of the profit actually achieved. Real example found live:
// an XAUUSD ema_pullback trade reached $39.40 of a $58.88 target (66.9% —
// just under the 70% gate) and gave it ALL back to $0.01, because nothing
// engaged.
//
// This replays every real XAUUSD trade (any strategy) from its actual open
// price/time using real M1 candles, and tests a retention-based rule instead:
// once peak gain reaches the SAME 50% arm point already used for breakeven,
// close the trade if current gain falls to <= retentionPct of peak gain
// (rather than only protecting entry price). Compares several retention
// percentages against what the strategy's own original SL/TP would have
// produced with NO retention rule at all (pure SL/TP race, the closest
// available proxy for "no protection").
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

type Trade struct {
	Strategy  string
	Side      string
	OpenPrice float64
	SL, TP    float64
	OpenTime  time.Time
}

type Candle struct {
	BarTime         time.Time
	Open, High, Low float64
}

const (
	armPct       = 50.0 // matches existing breakEvenTriggerPct
	simWindowHrs = 24
	dollarPerPt  = 1.0
)

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

type outcome struct {
	closeGain float64 // signed $ gain at close, in trade-direction terms
	hitTP     bool
}

// simulate walks M1 candles minute by minute from open, tracking running
// peak gain, and applies a retention rule once armed. If retentionPct<0,
// no retention rule is applied at all (pure race to original SL/TP).
func simulate(candles []Candle, startIdx int, t Trade, retentionPct float64) outcome {
	isBuy := t.Side == "BUY"
	tpDist := t.TP - t.OpenPrice
	if !isBuy {
		tpDist = t.OpenPrice - t.TP
	}
	if tpDist <= 0 {
		return outcome{}
	}
	armGain := tpDist * (armPct / 100)
	endTime := t.OpenTime.Add(simWindowHrs * time.Hour)

	peakGain := 0.0
	armed := false

	for i := startIdx; i < len(candles); i++ {
		c := candles[i]
		if c.BarTime.Before(t.OpenTime) {
			continue
		}
		if c.BarTime.After(endTime) {
			break
		}

		// Check SL/TP touch first (conservative: SL wins on same-candle ambiguity).
		var tpHit, slHit bool
		if isBuy {
			tpHit, slHit = c.High >= t.TP, c.Low <= t.SL
		} else {
			tpHit, slHit = c.Low <= t.TP, c.High >= t.SL
		}
		if slHit && tpHit {
			return outcome{closeGain: (t.SL - t.OpenPrice) * signOf(isBuy)}
		}
		if slHit {
			return outcome{closeGain: (t.SL - t.OpenPrice) * signOf(isBuy)}
		}
		if tpHit {
			return outcome{closeGain: tpDist, hitTP: true}
		}

		if retentionPct < 0 {
			continue // no retention rule — pure SL/TP race
		}

		// Track peak gain using the FAVORABLE extreme within this candle.
		var favorablePrice float64
		if isBuy {
			favorablePrice = c.High
		} else {
			favorablePrice = c.Low
		}
		gain := (favorablePrice - t.OpenPrice) * signOf(isBuy)
		if gain > peakGain {
			peakGain = gain
		}
		if !armed && peakGain >= armGain {
			armed = true
		}
		if armed {
			floor := peakGain * (retentionPct / 100)
			// current gain at the ADVERSE extreme of this candle (conservative)
			var adversePrice float64
			if isBuy {
				adversePrice = c.Low
			} else {
				adversePrice = c.High
			}
			currentGain := (adversePrice - t.OpenPrice) * signOf(isBuy)
			if currentGain <= floor {
				return outcome{closeGain: floor}
			}
		}
	}
	return outcome{} // unresolved within window — excluded from totals
}

func signOf(isBuy bool) float64 {
	if isBuy {
		return 1
	}
	return -1
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: peak_retention_sim <postgres-url>")
		os.Exit(1)
	}
	db, err := sql.Open("pgx", os.Args[1])
	must(err)
	defer db.Close()
	ctx := context.Background()

	trades := []Trade{}
	{
		rows, err := db.QueryContext(ctx, `
			SELECT COALESCE(sig.strategy,''), p.side, p.open_price, o.sl, o.tp, p.open_timestamp
			FROM positions p
			JOIN orders o ON o.id = p.our_order_id
			LEFT JOIN signals sig ON sig.id = o.signal_id
			JOIN symbols s ON s.id = p.symbol_id
			WHERE s.symbol='XAUUSD' AND o.sl IS NOT NULL AND o.tp IS NOT NULL
			  AND p.open_timestamp >= now() - interval '60 days'
			ORDER BY p.open_timestamp ASC`)
		must(err)
		for rows.Next() {
			var t Trade
			must(rows.Scan(&t.Strategy, &t.Side, &t.OpenPrice, &t.SL, &t.TP, &t.OpenTime))
			trades = append(trades, t)
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
	fmt.Fprintf(os.Stderr, "loaded %d trades, %d M1 candles\n", len(trades), len(candles))

	candleIdxFor := func(bt time.Time) int {
		return sort.Search(len(candles), func(i int) bool { return !candles[i].BarTime.Before(bt) })
	}

	retentions := []float64{-1, 10, 15, 20, 25, 30, 35, 40, 50, 60, 70}
	totals := make(map[float64]float64, len(retentions))
	counts := make(map[float64]int, len(retentions))
	tpHits := make(map[float64]int, len(retentions))

	// Paired comparison: for each trade, did retention=25 do better or
	// worse than no retention at all, on that SAME trade?
	pairedBetter, pairedWorse, pairedSame := 0, 0, 0
	var pairedDelta float64
	const pairedCandidate = 25.0

	for _, t := range trades {
		idx := candleIdxFor(t.OpenTime)
		none := simulate(candles, idx, t, -1)
		for _, r := range retentions {
			o := simulate(candles, idx, t, r)
			if o.closeGain == 0 && !o.hitTP {
				continue // unresolved
			}
			totals[r] += o.closeGain * dollarPerPt
			counts[r]++
			if o.hitTP {
				tpHits[r]++
			}
		}
		if none.closeGain == 0 && !none.hitTP {
			continue
		}
		cand := simulate(candles, idx, t, pairedCandidate)
		if cand.closeGain == 0 && !cand.hitTP {
			continue
		}
		candGain := cand.closeGain
		if cand.hitTP {
			candGain = t.TP - t.OpenPrice
			if t.Side != "BUY" {
				candGain = t.OpenPrice - t.TP
			}
		}
		noneGain := none.closeGain
		delta := candGain - noneGain
		pairedDelta += delta
		switch {
		case delta > 0.01:
			pairedBetter++
		case delta < -0.01:
			pairedWorse++
		default:
			pairedSame++
		}
	}

	fmt.Printf("\n%-22s %8s %8s %12s %10s\n", "retention", "n", "tp_hits", "total_$", "avg_$/trade")
	for _, r := range retentions {
		label := "none (pure SL/TP)"
		if r >= 0 {
			label = fmt.Sprintf("keep %.0f%% of peak", r)
		}
		avg := 0.0
		if counts[r] > 0 {
			avg = totals[r] / float64(counts[r])
		}
		fmt.Printf("%-22s %8d %8d %12.2f %10.2f\n", label, counts[r], tpHits[r], totals[r], avg)
	}

	fmt.Printf("\npaired vs none, retention=%.0f%%: better=%d worse=%d same=%d, net delta=%.2f\n",
		pairedCandidate, pairedBetter, pairedWorse, pairedSame, pairedDelta)
}
