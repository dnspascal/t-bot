// Scalping grid on real recorded ticks: every config trades one position at a time on bid/ask with latency and
// commission, and is scored on an in-sample and an out-of-sample half.
// Usage: go run ./research/cmd/scalp <ticks.tsv> <out.csv> [split yyyy-mm-dd]
package main

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	commission = 0.08 // $ per 1 oz round trip (0.04 open + 0.04 close, from prod fills)
	latencyMs  = 300  // market orders fill on the first tick at least this long after the signal
	maxSpread  = 0.50 // no entries while the spread is abnormally wide
	gapMs      = 30 * 60 * 1000
)

type tick struct {
	ms       int64
	bid, ask float64
}

func (t tick) mid() float64 { return (t.bid + t.ask) / 2 }

type config struct {
	family     string  // mom, rev, brk, fade
	win        int     // mom/rev: lookback seconds; brk/fade: lookback minutes
	move       float64 // mom/rev: $ move that triggers
	tp, sl     float64 // $ from entry
	hold       int     // max hold seconds
	activeOnly bool    // only 07:00-19:00 UTC (10:00-22:00 EAT)
}

func (c config) name() string {
	h := "all"
	if c.activeOnly {
		h = "act"
	}
	if c.family == "mom" || c.family == "rev" {
		return fmt.Sprintf("%s_w%ds_x%.1f_tp%.1f_sl%.1f_h%ds_%s", c.family, c.win, c.move, c.tp, c.sl, c.hold, h)
	}
	return fmt.Sprintf("%s_n%dm_tp%.1f_sl%.1f_h%ds_%s", c.family, c.win, c.tp, c.sl, c.hold, h)
}

type stats struct {
	n, wins          int
	net, gross, cost float64
	month            map[string]float64
	hold             float64
}

type result struct {
	cfg      config
	ins, oos stats
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: scalp <ticks.tsv> <out.csv> [split yyyy-mm-dd]")
		os.Exit(1)
	}
	ticks := load(os.Args[1])
	split := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	if len(os.Args) > 3 {
		split, _ = time.Parse(time.DateOnly, os.Args[3])
	}
	fmt.Printf("%d ticks %s .. %s, split at %s\n", len(ticks), ts(ticks[0].ms), ts(ticks[len(ticks)-1].ms), split.Format(time.DateOnly))

	cfgs := grid()
	fmt.Printf("%d configs on %d CPUs\n", len(cfgs), runtime.NumCPU())
	brkLevels := map[int][2][]float64{}
	for _, n := range []int{5, 15, 30} {
		brkLevels[n] = rollingLevels(ticks, n)
	}

	results := make([]result, len(cfgs))
	var wg sync.WaitGroup
	jobs := make(chan int)
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = simulate(ticks, cfgs[i], brkLevels, split.UnixMilli())
			}
		}()
	}
	start := time.Now()
	for i := range cfgs {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	fmt.Printf("simulated in %s\n\n", time.Since(start).Round(time.Second))

	write(os.Args[2], results)
	report(results)
}

func load(path string) []tick {
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	var out []tick
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Split(sc.Text(), "\t")
		if len(p) < 3 {
			continue
		}
		ms, err := strconv.ParseInt(p[0], 10, 64)
		if err != nil {
			continue
		}
		bid, _ := strconv.ParseFloat(p[1], 64)
		ask, _ := strconv.ParseFloat(p[2], 64)
		if bid <= 0 || ask < bid {
			continue
		}
		out = append(out, tick{ms, bid, ask})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ms < out[j].ms })
	return out
}

func grid() []config {
	var cs []config
	tps := []float64{0.3, 0.6, 1.0, 2.0}
	sls := []float64{0.3, 0.6, 1.0, 2.0}
	holds := []int{60, 300, 900}
	for _, act := range []bool{false, true} {
		for _, fam := range []string{"mom", "rev"} {
			for _, w := range []int{15, 30, 60, 120} {
				for _, x := range []float64{0.5, 1.0, 1.5, 2.5} {
					for _, tp := range tps {
						for _, sl := range sls {
							for _, h := range holds {
								cs = append(cs, config{fam, w, x, tp, sl, h, act})
							}
						}
					}
				}
			}
		}
		for _, fam := range []string{"brk", "fade"} {
			for _, n := range []int{5, 15, 30} {
				for _, tp := range tps {
					for _, sl := range sls {
						for _, h := range holds {
							cs = append(cs, config{fam, n, 0, tp, sl, h, act})
						}
					}
				}
			}
		}
	}
	return cs
}

// rollingLevels returns, per tick, the high and low of mid over the previous n whole minutes (NaN if unknown).
func rollingLevels(ticks []tick, n int) [2][]float64 {
	first := ticks[0].ms / 60000
	last := ticks[len(ticks)-1].ms / 60000
	size := last - first + 1
	mh, ml := make([]float64, size), make([]float64, size)
	for i := range mh {
		mh[i], ml[i] = math.NaN(), math.NaN()
	}
	for _, t := range ticks {
		m := t.ms/60000 - first
		v := t.mid()
		if math.IsNaN(mh[m]) || v > mh[m] {
			mh[m] = v
		}
		if math.IsNaN(ml[m]) || v < ml[m] {
			ml[m] = v
		}
	}
	rh, rl := make([]float64, size), make([]float64, size)
	for m := int64(0); m < size; m++ {
		hi, lo, seen := math.Inf(-1), math.Inf(1), 0
		for k := m - int64(n); k < m; k++ {
			if k < 0 || math.IsNaN(mh[k]) {
				continue
			}
			hi, lo, seen = max(hi, mh[k]), min(lo, ml[k]), seen+1
		}
		if seen < n/2 {
			hi, lo = math.NaN(), math.NaN()
		}
		rh[m], rl[m] = hi, lo
	}
	hiT, loT := make([]float64, len(ticks)), make([]float64, len(ticks))
	for i, t := range ticks {
		m := t.ms/60000 - first
		hiT[i], loT[i] = rh[m], rl[m]
	}
	return [2][]float64{hiT, loT}
}

func active(ms int64) bool {
	t := time.UnixMilli(ms).UTC()
	return t.Hour() >= 7 && t.Hour() < 19
}

func simulate(ticks []tick, c config, lv map[int][2][]float64, split int64) result {
	r := result{cfg: c, ins: stats{month: map[string]float64{}}, oos: stats{month: map[string]float64{}}}
	var levels [2][]float64
	if c.family == "brk" || c.family == "fade" {
		levels = lv[c.win]
	}
	back := 0
	prevSig := 0
	inPos, pendingDir := false, 0
	var pendingAt, openedMs int64
	var dir int
	var entry float64
	exitPending := false
	var exitAt int64

	closeAt := func(t tick, px float64) {
		gross := px - entry
		if dir < 0 {
			gross = entry - px
		}
		s := &r.ins
		if openedMs >= split {
			s = &r.oos
		}
		s.n++
		s.gross += gross
		s.net += gross - commission
		s.cost += commission
		if gross-commission > 0 {
			s.wins++
		}
		s.month[time.UnixMilli(openedMs).UTC().Format("2006-01")] += gross - commission
		s.hold += float64(t.ms-openedMs) / 1000
		inPos, exitPending = false, false
	}

	for i, t := range ticks {
		if i > 0 && t.ms-ticks[i-1].ms > gapMs {
			if inPos {
				if dir > 0 {
					closeAt(t, t.bid)
				} else {
					closeAt(t, t.ask)
				}
			}
			pendingDir, prevSig = 0, 0
		}
		if inPos {
			if exitPending {
				if t.ms >= exitAt {
					if dir > 0 {
						closeAt(t, t.bid)
					} else {
						closeAt(t, t.ask)
					}
				}
			} else if dir > 0 {
				switch {
				case t.bid >= entry+c.tp:
					closeAt(t, t.bid)
				case t.bid <= entry-c.sl:
					closeAt(t, t.bid)
				case t.ms-openedMs >= int64(c.hold)*1000:
					exitPending, exitAt = true, t.ms+latencyMs
				}
			} else {
				switch {
				case t.ask <= entry-c.tp:
					closeAt(t, t.ask)
				case t.ask >= entry+c.sl:
					closeAt(t, t.ask)
				case t.ms-openedMs >= int64(c.hold)*1000:
					exitPending, exitAt = true, t.ms+latencyMs
				}
			}
		}
		if !inPos && pendingDir != 0 && t.ms >= pendingAt {
			dir, inPos, openedMs = pendingDir, true, t.ms
			entry = t.ask
			if dir < 0 {
				entry = t.bid
			}
			pendingDir = 0
		}

		sig := 0
		switch c.family {
		case "mom", "rev":
			for back < i && ticks[back].ms <= t.ms-int64(c.win)*1000 {
				back++
			}
			if back > 0 {
				d := t.mid() - ticks[back-1].mid()
				if d >= c.move {
					sig = 1
				} else if d <= -c.move {
					sig = -1
				}
			}
			if c.family == "rev" {
				sig = -sig
			}
		case "brk", "fade":
			hi, lo := levels[0][i], levels[1][i]
			if !math.IsNaN(hi) {
				if t.mid() > hi {
					sig = 1
				} else if t.mid() < lo {
					sig = -1
				}
			}
			if c.family == "fade" {
				sig = -sig
			}
		}
		fresh := sig != 0 && sig != prevSig
		prevSig = sig
		if fresh && !inPos && pendingDir == 0 && t.ask-t.bid <= maxSpread && (!c.activeOnly || active(t.ms)) {
			pendingDir, pendingAt = sig, t.ms+latencyMs
		}
	}
	return r
}

func write(path string, rs []result) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Write([]string{"config", "family", "is_n", "is_net", "is_gross", "is_win", "oos_n", "oos_net", "oos_gross", "oos_win", "avg_hold_s"})
	for _, r := range rs {
		n := r.ins.n + r.oos.n
		hold := 0.0
		if n > 0 {
			hold = (r.ins.hold + r.oos.hold) / float64(n)
		}
		w.Write([]string{r.cfg.name(), r.cfg.family, strconv.Itoa(r.ins.n), f2(r.ins.net), f2(r.ins.gross), strconv.Itoa(r.ins.wins),
			strconv.Itoa(r.oos.n), f2(r.oos.net), f2(r.oos.gross), strconv.Itoa(r.oos.wins), f2(hold)})
	}
	w.Flush()
}

func report(rs []result) {
	per := func(s stats) float64 {
		if s.n == 0 {
			return 0
		}
		return s.net / float64(s.n)
	}
	grossPer := func(s stats) float64 {
		if s.n == 0 {
			return 0
		}
		return s.gross / float64(s.n)
	}
	pos := 0
	posBoth := 0
	for _, r := range rs {
		if r.ins.net+r.oos.net > 0 {
			pos++
		}
		if r.ins.net > 0 && r.oos.net > 0 {
			posBoth++
		}
	}
	fmt.Printf("%d configs: %d net positive overall, %d net positive in BOTH halves\n\n", len(rs), pos, posBoth)

	fams := map[string][]result{}
	for _, r := range rs {
		fams[r.cfg.family] = append(fams[r.cfg.family], r)
	}
	fmt.Printf("%-6s %8s %12s %12s %12s %10s\n", "family", "configs", "med gross/tr", "med net/tr", "best net/tr", "pos both")
	for _, f := range []string{"mom", "rev", "brk", "fade"} {
		var g, n []float64
		best, both := math.Inf(-1), 0
		for _, r := range fams[f] {
			all := stats{n: r.ins.n + r.oos.n, net: r.ins.net + r.oos.net, gross: r.ins.gross + r.oos.gross}
			g, n = append(g, grossPer(all)), append(n, per(all))
			best = max(best, per(all))
			if r.ins.net > 0 && r.oos.net > 0 {
				both++
			}
		}
		fmt.Printf("%-6s %8d %12.3f %12.3f %12.3f %10d\n", f, len(fams[f]), median(g), median(n), best, both)
	}

	line := func(r result) string {
		n := r.ins.n + r.oos.n
		return fmt.Sprintf("%-44s %6d %7.3f %7.3f %8.0f %6d %7.3f %8.0f %5.0f%% %6.0fs",
			r.cfg.name(), r.ins.n, grossPer(r.ins), per(r.ins), r.ins.net, r.oos.n, per(r.oos), r.oos.net,
			100*float64(r.ins.wins+r.oos.wins)/math.Max(1, float64(n)), (r.ins.hold+r.oos.hold)/math.Max(1, float64(n)))
	}
	hdr := fmt.Sprintf("%-44s %6s %7s %7s %8s %6s %7s %8s %6s %7s", "config (min 200 trades in-sample)", "IS n", "IS grs", "IS net", "IS $", "OOS n", "OOS net", "OOS $", "win", "hold")

	cand := []result{}
	for _, r := range rs {
		if r.ins.n >= 200 {
			cand = append(cand, r)
		}
	}
	sort.Slice(cand, func(i, j int) bool { return cand[i].ins.net > cand[j].ins.net })
	fmt.Printf("\nbest 15 chosen on in-sample $, then judged out-of-sample ($ per 1 oz, after spread + $0.08 commission):\n%s\n", hdr)
	for _, r := range cand[:min(15, len(cand))] {
		fmt.Println(line(r))
	}
	var oos []float64
	for _, r := range cand[:min(15, len(cand))] {
		oos = append(oos, r.oos.net)
	}
	fmt.Printf("median out-of-sample $ of those 15: %.0f\n", median(oos))

	sort.Slice(cand, func(i, j int) bool { return cand[i].ins.n+cand[i].oos.n > cand[j].ins.n+cand[j].oos.n })
	fmt.Printf("\nmost active 10 (trades per hour matter for scalping):\n%s\n", hdr)
	for _, r := range cand[:min(10, len(cand))] {
		fmt.Println(line(r))
	}
}

func median(x []float64) float64 {
	if len(x) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), x...)
	sort.Float64s(s)
	return s[len(s)/2]
}

func f2(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

func ts(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04") }
