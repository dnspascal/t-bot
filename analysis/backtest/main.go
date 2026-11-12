// Backtest on real cTrader history: replays bars through the live indicator
// calculator and strategy code, entering on M5 closes and managing exits on M1
// the way internal/bot does (SL/TP, breakeven, peak drawback, time stops, EOD).
//
//	go run ./analysis/backtest fetch <dir> <from YYYY-MM-DD>   (needs CTRADER_* env)
//	go run ./analysis/backtest run <dir> <strategy,...> [spread]
package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
	"github.com/denismgaya/t-bot/internal/strategy/breakout"
	ddoversoldbounce "github.com/denismgaya/t-bot/internal/strategy/dd_oversold_bounce"
	emapullback "github.com/denismgaya/t-bot/internal/strategy/ema_pullback"
	sessionmomentum "github.com/denismgaya/t-bot/internal/strategy/session_momentum"
	trendfollow "github.com/denismgaya/t-bot/internal/strategy/trend_follow"
)

// Mirrors internal/bot (watcher.go, registry.go, bot.go) for XAUUSD.
const (
	pipSize                = 0.01
	breakEvenTriggerPct    = 50.0
	breakEvenBufferPips    = 5.0
	peakRetentionPct       = 25.0
	neverProfitableTimeout = 30 * time.Minute
	stallTimeout           = 60 * time.Minute
	stallMaxFavPct         = 15.0
	maxPerStrategy         = 2
	minScaleInPips         = 2
	bufferSize             = 100
)

var strategies = map[string]func() strategy.Strategy{
	"breakout":           func() strategy.Strategy { return breakout.New() },
	"dd_oversold_bounce": func() strategy.Strategy { return ddoversoldbounce.New() },
	"ema_pullback":       func() strategy.Strategy { return emapullback.New() },
	"session_momentum":   func() strategy.Strategy { return sessionmomentum.New() },
	"trend_follow":       func() strategy.Strategy { return trendfollow.New() },
}

type bar struct {
	t                      int64
	open, high, low, close float64
	vol                    int64
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: backtest fetch <dir> <from> | run <dir> <strategy,...> [spread]")
		os.Exit(1)
	}
	switch os.Args[1] {
	case "fetch":
		from, err := time.Parse(time.DateOnly, os.Args[3])
		must(err)
		must(fetch(os.Args[2], from))
	case "run":
		spread := 0.30
		if len(os.Args) > 4 {
			spread, _ = strconv.ParseFloat(os.Args[4], 64)
		}
		for _, name := range strings.Split(os.Args[3], ",") {
			run(os.Args[2], name, spread)
		}
	}
}

type period struct {
	name  string
	dur   int64
	bars  []bar
	next  int
	calc  *indicator.Calculator
	hist  []indicator.OHLC
	close []float64
	vols  []int64
}

func (p *period) feed(b bar) indicator.MarketState {
	ms := p.calc.Calculate("XAUUSD", "ctrader", p.name, b.t, b.open, b.high, b.low, b.close, b.vol, p.close, p.vols, p.hist)
	p.hist = append(p.hist, indicator.OHLC{Open: b.open, High: b.high, Low: b.low, Close: b.close})
	p.close = append(p.close, b.close)
	p.vols = append(p.vols, b.vol)
	if len(p.hist) > bufferSize {
		p.hist, p.close, p.vols = p.hist[1:], p.close[1:], p.vols[1:]
	}
	return ms
}

type position struct {
	side                string
	entry, sl, tp, risk float64
	opened              int64
	peak                float64 // most favourable price seen
	beMoved             bool
	sl0                 float64 // SL at entry; sl moves to breakeven
	snap                string
}

type trade struct {
	side           string
	opened, closed int64
	pnl, r, mfeR   float64
	reason         string
	entry, sl, tp  float64
	snap           string // H1/M15 states at entry, JSON
}

func run(dir, name string, spread float64) {
	mk, ok := strategies[name]
	if !ok {
		fmt.Fprintln(os.Stderr, "unknown strategy", name)
		os.Exit(1)
	}
	s := mk()
	aware, _ := s.(strategy.OutcomeAware)

	ps := []*period{{name: "H1", dur: 3600}, {name: "M15", dur: 900}, {name: "M5", dur: 300}}
	for _, p := range ps {
		p.bars = load(filepath.Join(dir, p.name+".csv"))
		p.calc = indicator.NewCalculator()
	}
	m1 := load(filepath.Join(dir, "M1.csv"))

	states := map[string]indicator.MarketState{}
	var open []*position
	var trades []trade
	warmFrom := m1[0].t + 3600*bufferSize // first 100 H1 bars only warm the indicators

	closePos := func(p *position, at int64, price float64, reason string) {
		pnl := price - p.entry
		if p.side == config.SignalSell {
			pnl = -pnl
		}
		mfe := p.peak - p.entry
		if p.side == config.SignalSell {
			mfe = -mfe
		}
		trades = append(trades, trade{p.side, p.opened, at, pnl, pnl / p.risk, mfe / p.risk, reason, p.entry, p.sl0, p.tp, p.snap})
		if aware != nil {
			aware.OnClosed(p.side, reason, time.Unix(at, 0))
		}
	}

	for _, b := range m1 {
		m5Closed := false
		for _, p := range ps {
			for p.next < len(p.bars) && p.bars[p.next].t+p.dur <= b.t {
				states[p.name] = p.feed(p.bars[p.next])
				p.next++
				if p.name == "M5" {
					m5Closed = true
				}
			}
		}
		now := time.Unix(b.t, 0).UTC()
		eod := now.Weekday() != time.Saturday && now.Weekday() != time.Sunday && now.Hour() == 21 && now.Minute() >= 30

		if eod {
			for _, p := range open {
				closePos(p, b.t, b.open, strategy.CloseReasonEODClose)
			}
			open = nil
		}

		if m5Closed && !eod && b.t >= warmFrom {
			r := s.Evaluate(states, b.open, pipSize)
			if (r.Signal == config.SignalBuy || r.Signal == config.SignalSell) && canOpen(open, r.Signal, b.open) {
				entry := b.open
				if r.Signal == config.SignalBuy {
					entry += spread // buy at ask; exits are on bid
				}
				open = append(open, &position{side: r.Signal, entry: entry, sl: r.SLPrice, tp: r.TPPrice,
					risk: math.Abs(entry - r.SLPrice), opened: b.t, peak: entry, sl0: r.SLPrice, snap: snapshot(states)})
			}
		}

		kept := open[:0]
		for _, p := range open {
			if reason, px, done := step(p, b, spread); done {
				closePos(p, b.t+60, px, reason)
				continue
			}
			kept = append(kept, p)
		}
		open = kept
	}
	report(name, trades)
}

func canOpen(open []*position, side string, price float64) bool {
	if len(open) >= maxPerStrategy {
		return false
	}
	for _, p := range open {
		if p.side != side {
			return false
		}
		if side == config.SignalBuy && price < p.entry+minScaleInPips*pipSize {
			return false
		}
		if side == config.SignalSell && price > p.entry-minScaleInPips*pipSize {
			return false
		}
	}
	return true
}

// step advances one M1 bar. SL is checked before TP inside a bar (worst case).
// SELL positions close at ask, so their stops/targets are compared against bar+spread.
func step(p *position, b bar, spread float64) (string, float64, bool) {
	buy := p.side == config.SignalBuy
	hi, lo, cl := b.high, b.low, b.close
	if !buy {
		hi, lo, cl = hi+spread, lo+spread, cl+spread
	}
	if buy && lo <= p.sl || !buy && hi >= p.sl {
		if p.beMoved {
			return strategy.CloseReasonBreakevenSL, p.sl, true
		}
		return strategy.CloseReasonSLHit, p.sl, true
	}
	if buy && hi >= p.tp || !buy && lo <= p.tp {
		return strategy.CloseReasonTPHit, p.tp, true
	}

	if buy {
		p.peak = max(p.peak, hi)
	} else {
		p.peak = min(p.peak, lo)
	}
	tpDist, peakGain, gain := p.tp-p.entry, p.peak-p.entry, cl-p.entry
	if !buy {
		tpDist, peakGain, gain = -tpDist, -peakGain, -gain
	}
	if tpDist <= 0 {
		return "", 0, false
	}
	armed := peakGain >= tpDist*breakEvenTriggerPct/100
	if armed && !p.beMoved {
		p.beMoved = true
		if buy {
			p.sl = p.entry + breakEvenBufferPips*pipSize
		} else {
			p.sl = p.entry - breakEvenBufferPips*pipSize
		}
	}
	if armed && peakGain > 0 && gain <= peakGain*peakRetentionPct/100 {
		return strategy.PeakDrawbackPrefix + "sim", cl, true
	}

	age := time.Duration(b.t+60-p.opened) * time.Second
	if age >= neverProfitableTimeout && peakGain <= pipSize {
		return strategy.CloseReasonTimeStopNeverProfitable30m, cl, true
	}
	if age >= stallTimeout && peakGain/tpDist*100 < stallMaxFavPct {
		return strategy.CloseReasonTimeStopStalled60m, cl, true
	}
	return "", 0, false
}

func report(name string, ts []trade) {
	fmt.Printf("\n===== %s  (%d trades)\n", name, len(ts))
	if len(ts) == 0 {
		return
	}
	type agg struct {
		n, w, tp, sl int
		pnl, r       float64
	}
	add := func(a *agg, t trade) {
		a.n++
		a.pnl += t.pnl
		a.r += t.r
		if t.pnl > 0 {
			a.w++
		}
		if t.reason == strategy.CloseReasonTPHit {
			a.tp++
		}
		if t.reason == strategy.CloseReasonSLHit {
			a.sl++
		}
	}
	line := func(k string, a agg) {
		fmt.Printf("  %-12s n=%4d won=%4d (%3.0f%%) tp=%3d sl=%3d  points=%8.2f  R=%7.2f  avgR=%6.3f\n",
			k, a.n, a.w, 100*float64(a.w)/float64(a.n), a.tp, a.sl, a.pnl, a.r, a.r/float64(a.n))
	}
	months, sides, reasons := map[string]*agg{}, map[string]*agg{}, map[string]*agg{}
	var all agg
	for _, t := range ts {
		m := time.Unix(t.opened, 0).UTC().Format("2006-01")
		for _, mp := range []struct {
			m map[string]*agg
			k string
		}{{months, m}, {sides, t.side}, {reasons, strings.SplitN(t.reason, "=", 2)[0]}} {
			if mp.m[mp.k] == nil {
				mp.m[mp.k] = &agg{}
			}
			add(mp.m[mp.k], t)
		}
		add(&all, t)
	}
	for _, grp := range []map[string]*agg{months, sides, reasons} {
		keys := make([]string, 0, len(grp))
		for k := range grp {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			line(k, *grp[k])
		}
		fmt.Println()
	}
	line("ALL", all)
	var mfeSL []float64
	for _, t := range ts {
		if t.reason == strategy.CloseReasonSLHit {
			mfeSL = append(mfeSL, t.mfeR)
		}
	}
	if len(mfeSL) > 0 {
		sort.Float64s(mfeSL)
		fmt.Printf("  SL trades: median best move before stopping = %.2fR\n", mfeSL[len(mfeSL)/2])
	}
	if os.Getenv("BT_TRADES") != "" {
		f, _ := os.Create(filepath.Join(os.Getenv("BT_TRADES"), name+"_trades.csv"))
		w := csv.NewWriter(f)
		for _, t := range ts {
			w.Write([]string{t.side, strconv.FormatInt(t.opened, 10), strconv.FormatInt(t.closed, 10), ff(t.pnl), ff(t.r), ff(t.mfeR), t.reason, ff(t.entry), ff(t.sl), ff(t.tp), t.snap})
		}
		w.Flush()
		f.Close()
	}
}

func load(path string) []bar {
	f, err := os.Open(path)
	must(err)
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	must(err)
	out := make([]bar, 0, len(rows))
	for _, r := range rows {
		t, _ := strconv.ParseInt(r[0], 10, 64)
		v := make([]float64, 4)
		for i := range v {
			v[i], _ = strconv.ParseFloat(r[i+1], 64)
		}
		vol, _ := strconv.ParseInt(r[5], 10, 64)
		out = append(out, bar{t, v[0], v[1], v[2], v[3], vol})
	}
	return out
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func snapshot(states map[string]indicator.MarketState) string {
	out := map[string]indicator.MarketState{}
	for _, k := range []string{"H1", "M15", "M5"} {
		out[k] = states[k]
	}
	b, _ := json.Marshal(out)
	return string(b)
}
