package main

// Replays every closed XAUUSD trade on real M1 candles from open to its first close
// fill and tests arming breakeven + peak retention at min(50% of TP distance, k x SL risk)
// instead of 50% of TP alone. Arming earlier only adds exits, so a trade either closes
// earlier under the new rule or keeps its real result.
// Run: go run ./analysis/daily/2026-11-16/queries/early_arm_sim <postgres-url>
import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	tpArmPct     = 50.0 // breakEvenTriggerPct
	retentionPct = 25.0 // peakRetentionPct
	bufferPrice  = 0.5  // 5 pips x 0.10
)

type trade struct {
	strategy, side           string
	open, sl, tp, oz         float64
	openT, firstCloseT       time.Time
	realNet, realGross, fees float64
	reason                   string
}

type candle struct {
	t       time.Time
	h, l, c float64
}

// sim returns (exited, gain per oz, exit time) if the protection rule closes the trade before its real first close.
func sim(cs []candle, t trade, armGain float64) (bool, float64, time.Time) {
	return sim2(cs, t, armGain, armGain)
}

// sim2 arms the breakeven stop at beArm and the peak-retention floor at retArm.
func sim2(cs []candle, t trade, beArm, retArm float64) (bool, float64, time.Time) {
	buy := t.side == "BUY"
	g := func(p float64) float64 {
		if buy {
			return p - t.open
		}
		return t.open - p
	}
	peak, be, ret := 0.0, false, false
	level := func() float64 {
		if ret {
			return math.Max(bufferPrice, peak*retentionPct/100)
		}
		return bufferPrice
	}
	for _, c := range cs {
		fav, adv := c.l, c.h
		if buy {
			fav, adv = c.h, c.l
		}
		if be || ret {
			if stop := level(); g(adv) <= stop {
				return true, stop, c.t
			}
		}
		if g(fav) > peak {
			peak = g(fav)
		}
		be = be || peak >= beArm
		ret = ret || peak >= retArm
		if ret {
			if stop := level(); g(c.c) <= stop {
				return true, math.Min(g(c.c), stop), c.t
			}
		}
	}
	return false, 0, time.Time{}
}

func main() {
	beOnly := len(os.Args) > 2 && os.Args[2] == "be-only"
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: early_arm_sim <postgres-url>")
		os.Exit(1)
	}
	db, err := sql.Open("pgx", os.Args[1])
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, `
		WITH c AS (SELECT our_position_id pid, string_agg(DISTINCT close_reason,'|') reason, sum(net_profit) net, sum(gross_profit) gross,
		             min(provider_exec_time) first_close
		           FROM fills WHERE gross_profit IS NOT NULL AND closed_volume>0 GROUP BY 1)
		SELECT COALESCE(sig.strategy,''), p.side, p.open_price, o.sl, o.tp, p.volume/100.0, p.open_timestamp, c.first_close, c.net, c.gross, c.reason
		FROM positions p JOIN c ON c.pid=p.id JOIN orders o ON o.id=p.our_order_id JOIN symbols s ON s.id=p.symbol_id
		LEFT JOIN signals sig ON sig.id=o.signal_id
		WHERE s.symbol='XAUUSD' AND p.open_timestamp >= '2026-10-19 01:01+03' AND c.reason <> 'test_amend_cmd'
		  AND o.sl IS NOT NULL AND o.tp IS NOT NULL
		ORDER BY p.open_timestamp`)
	if err != nil {
		panic(err)
	}
	var trades []trade
	for rows.Next() {
		var t trade
		if err := rows.Scan(&t.strategy, &t.side, &t.open, &t.sl, &t.tp, &t.oz, &t.openT, &t.firstCloseT, &t.realNet, &t.realGross, &t.reason); err != nil {
			panic(err)
		}
		t.fees = t.realNet - t.realGross
		trades = append(trades, t)
	}

	crows, err := db.QueryContext(ctx, `SELECT bar_time, high, low, close FROM candles c JOIN symbols s ON s.id=c.symbol_id
		WHERE s.symbol='XAUUSD' AND period='M1' AND bar_time >= '2026-10-19' ORDER BY bar_time`)
	if err != nil {
		panic(err)
	}
	var all []candle
	for crows.Next() {
		var c candle
		if err := crows.Scan(&c.t, &c.h, &c.l, &c.c); err != nil {
			panic(err)
		}
		all = append(all, c)
	}
	fmt.Printf("trades %d, M1 candles %d\n\n", len(trades), len(all))

	window := func(t trade) []candle {
		start := t.openT.Truncate(time.Minute)
		end := t.firstCloseT.Truncate(time.Minute)
		i := sort.Search(len(all), func(i int) bool { return !all[i].t.Before(start) })
		var out []candle
		for ; i < len(all) && all[i].t.Before(end); i++ {
			out = append(out, all[i])
		}
		return out
	}

	type res struct {
		net                   float64
		changed, savedL, cutW int
		savedAmt, cutAmt      float64
		byStrat               map[string]float64
	}
	ks := []float64{0, 0.75, 1.0, 1.25, 1.5}
	results := map[float64]*res{}
	realTotal := 0.0
	realByStrat := map[string]float64{}
	for _, t := range trades {
		realTotal += t.realNet
		realByStrat[t.strategy] += t.realNet
	}
	var detail []string
	for _, k := range ks {
		r := &res{byStrat: map[string]float64{}}
		results[k] = r
		for _, t := range trades {
			tpd := math.Abs(t.tp - t.open)
			risk := math.Abs(t.sl - t.open)
			arm := tpd * tpArmPct / 100
			if k > 0 && k*risk < arm {
				arm = k * risk
			}
			cs := window(t)
			net := t.realNet
			if k > 0 {
				// only the extra arming band is new; the current rule already ran live
				curExit, _, _ := sim(cs, t, tpd*tpArmPct/100)
				exit, gain, at := sim(cs, t, arm)
				if beOnly {
					exit, gain, at = sim2(cs, t, arm, tpd*tpArmPct/100)
				}
				if exit && !curExit {
					net = gain*t.oz + t.fees
					r.changed++
					if net > t.realNet {
						r.savedL++
						r.savedAmt += net - t.realNet
					} else {
						r.cutW++
						r.cutAmt += t.realNet - net
					}
					if k == 1.0 {
						detail = append(detail, fmt.Sprintf("%s %-17s %-4s real %-22s %8.2f -> %8.2f  (exit %s)",
							t.openT.In(time.FixedZone("EAT", 3*3600)).Format("01-02 15:04"), t.strategy, t.side, t.reason, t.realNet, net,
							at.In(time.FixedZone("EAT", 3*3600)).Format("15:04")))
					}
				}
			}
			r.net += net
			r.byStrat[t.strategy] += net
		}
	}

	fmt.Printf("%-26s %10s %8s %16s %18s\n", "arm rule", "net $", "changed", "loss->better $", "win cut short $")
	for _, k := range ks {
		r := results[k]
		name := "current: 50% of TP"
		if k > 0 {
			name = fmt.Sprintf("min(50%% TP, %.2fx risk)", k)
		}
		fmt.Printf("%-26s %10.2f %8d %9d %6.2f %11d %6.2f\n", name, r.net, r.changed, r.savedL, r.savedAmt, r.cutW, r.cutAmt)
	}
	fmt.Printf("\n(real total %.2f)\n\nby strategy, net $:\n%-20s %10s", realTotal, "strategy", "current")
	for _, k := range ks[1:] {
		fmt.Printf(" %10s", fmt.Sprintf("%.2fxR", k))
	}
	fmt.Println()
	var names []string
	for s := range realByStrat {
		names = append(names, s)
	}
	sort.Strings(names)
	for _, s := range names {
		fmt.Printf("%-20s %10.2f", s, results[0].byStrat[s])
		for _, k := range ks[1:] {
			fmt.Printf(" %10.2f", results[k].byStrat[s])
		}
		fmt.Println()
	}
	fmt.Println("\ntrades changed at 1.0x risk:")
	for _, d := range detail {
		fmt.Println(d)
	}
}
