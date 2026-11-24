package main

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

// Research: tfx is trend_follow with every rule evaluated on its own and tunable. BT_TFX="adx=15,gap=0.4,er=0.3,..."
// changes a rule or adds an entry filter; BT_TFX_LOG=<file> writes each M5 evaluation with all rule verdicts and features.
type tfxParams struct {
	adx, gap, bounce float64
	streak           int
	noEMA50          bool
	noBreakout       bool
	noM5Regime       bool
	dir15            bool    // take the trade direction from the M15 regime instead of H1
	er, atrx         float64 // entry filters: min H1 efficiency ratio (12 bars), min H1 ATR vs its 50-bar mean
	e200             bool    // entry filter: price on the trade side of H1 EMA200
	hours            [2]int  // entry filter: EAT hour window [from, to), zero = off
}

type tfx struct {
	p                    tfxParams
	buyFails, sellFails  int
	cooldownDir          string
	cooldownUntil        time.Time
	lastM5Regime         string
	lastM5Bar, lastH1Bar int64
	streak, seen         int
	h1Closes, h1ATRs     []float64
	log                  *csv.Writer
}

func init() {
	strategies["tfx"] = func() strategy.Strategy { return newTFX(os.Getenv("BT_TFX"), os.Getenv("BT_TFX_LOG")) }
}

func newTFX(spec, logPath string) *tfx {
	s := &tfx{p: tfxParams{adx: 20, gap: 0.6, bounce: 0.5, streak: 2}}
	for _, kv := range strings.Split(spec, ",") {
		k, v, _ := strings.Cut(kv, "=")
		f, _ := strconv.ParseFloat(v, 64)
		switch k {
		case "adx":
			s.p.adx = f
		case "gap":
			s.p.gap = f
		case "bounce":
			s.p.bounce = f
		case "streak":
			s.p.streak = int(f)
		case "noema50":
			s.p.noEMA50 = true
		case "nobreakout":
			s.p.noBreakout = true
		case "nom5reg":
			s.p.noM5Regime = true
		case "dir15":
			s.p.dir15 = true
		case "er":
			s.p.er = f
		case "atrx":
			s.p.atrx = f
		case "e200":
			s.p.e200 = true
		case "hours":
			a, b, _ := strings.Cut(v, "-")
			s.p.hours[0], _ = strconv.Atoi(a)
			s.p.hours[1], _ = strconv.Atoi(b)
		}
	}
	if logPath != "" {
		f, err := os.Create(logPath)
		must(err)
		s.log = csv.NewWriter(f)
		s.log.Write([]string{"t", "dir", "signal", "h1_regime", "m5_ema", "m5_regime", "fresh", "adx", "ema50", "gap", "bounce",
			"cooldown", "h1_adx", "gap_ratio", "bounce_atr", "er12", "atrx", "e200", "hour"})
	}
	return s
}

func (s *tfx) Name() string           { return "tfx" }
func (s *tfx) UsesTrendWatcher() bool { return true }

func (s *tfx) OnClosed(side, reason string, at time.Time) {
	switch strategy.ClassifyCloseReason(reason) {
	case strategy.CloseInvalidated:
		n := &s.sellFails
		if side == config.SignalBuy {
			n = &s.buyFails
		}
		*n++
		if *n >= 2 {
			s.cooldownDir, s.cooldownUntil = side, at.Add(time.Hour)
		}
	case strategy.CloseValidated:
		if side == config.SignalBuy {
			s.buyFails = 0
		} else {
			s.sellFails = 0
		}
		if s.cooldownDir == side {
			s.cooldownDir = ""
		}
	}
}

func efficiency(c []float64, n int) float64 {
	if len(c) <= n {
		return 0
	}
	c = c[len(c)-n-1:]
	path := 0.0
	for i := 1; i < len(c); i++ {
		path += math.Abs(c[i] - c[i-1])
	}
	if path == 0 {
		return 0
	}
	return math.Abs(c[len(c)-1]-c[0]) / path
}

func (s *tfx) Evaluate(states map[string]indicator.MarketState, price, pipSize float64) strategy.EntryResult {
	hold := strategy.EntryResult{Signal: config.SignalHold}
	h1, m5, m15 := states[config.PeriodH1], states[config.PeriodM5], states[config.PeriodM15]
	if !h1.IsWarmedUp || !m5.IsWarmedUp {
		return hold
	}
	if h1.BarTime != s.lastH1Bar {
		s.lastH1Bar = h1.BarTime
		s.h1Closes, s.h1ATRs = append(s.h1Closes, h1.Close), append(s.h1ATRs, h1.ATR)
		if len(s.h1Closes) > 60 {
			s.h1Closes, s.h1ATRs = s.h1Closes[1:], s.h1ATRs[1:]
		}
	}
	dirTF := h1
	if s.p.dir15 {
		dirTF = m15
	}
	tracked := dirTF.Regime == config.TrendingUp || dirTF.Regime == config.TrendingDown || dirTF.Regime == config.Breakout
	if tracked && m5.BarTime != s.lastM5Bar { // live trend_follow only counts M5 bars while H1 has a direction
		if m5.Regime == s.lastM5Regime {
			s.streak++
		} else {
			s.lastM5Regime, s.streak = m5.Regime, 1
		}
		s.lastM5Bar = m5.BarTime
		s.seen++
	}
	var dir string
	switch dirTF.Regime {
	case config.TrendingUp:
		dir = config.SignalBuy
	case config.TrendingDown:
		dir = config.SignalSell
	case config.Breakout:
		if !s.p.noBreakout {
			dir = config.SignalSell
			if m5.EMAFast > m5.EMASlow {
				dir = config.SignalBuy
			}
		}
	}
	buy := dir == config.SignalBuy
	gap, peak, bounce := h1.EMAFast-h1.EMASlow, h1.RecentEMAGapMax, h1.RecentHigh-price
	if !buy {
		gap, peak, bounce = -gap, -h1.RecentEMAGapMin, price-h1.RecentLow
	}
	gapRatio, bounceATR := 0.0, 99.0
	if peak > 0 {
		gapRatio = gap / peak
	}
	if h1.ATR > 0 {
		bounceATR = bounce / h1.ATR
	}
	atrMean := 0.0
	for _, a := range s.h1ATRs {
		atrMean += a
	}
	atrx := 0.0
	if len(s.h1ATRs) > 0 && atrMean > 0 {
		atrx = h1.ATR / (atrMean / float64(len(s.h1ATRs)))
	}
	er := efficiency(s.h1Closes, 12)
	hour := time.Unix(m5.BarTime+300, 0).In(eatLoc).Hour()

	ok := map[string]bool{
		"h1_regime": dir != "",
		"m5_ema":    buy && m5.EMAFast >= m5.EMASlow || !buy && m5.EMAFast <= m5.EMASlow,
		"m5_regime": s.p.noM5Regime || buy && m5.Regime == config.TrendingUp || !buy && m5.Regime == config.TrendingDown,
		"fresh":     s.seen >= 3 && s.streak <= s.p.streak,
		"adx":       h1.ADX >= s.p.adx,
		"ema50":     s.p.noEMA50 || h1.EMA50 > 0 && (buy && h1.EMASlow > h1.EMA50 || !buy && h1.EMASlow < h1.EMA50),
		"gap":       peak > 0 && gapRatio >= s.p.gap,
		"bounce":    bounceATR < s.p.bounce,
		"cooldown":  !(dir == s.cooldownDir && m15.IsWarmedUp && time.Unix(m15.BarTime, 0).Before(s.cooldownUntil)),
	}
	filters := (s.p.er == 0 || er >= s.p.er) && (s.p.atrx == 0 || atrx >= s.p.atrx) &&
		(!s.p.e200 || h1.EMA200 > 0 && (buy && price > h1.EMA200 || !buy && price < h1.EMA200)) &&
		(s.p.hours[1] == 0 || hour >= s.p.hours[0] && hour < s.p.hours[1])
	pass := filters && m15.IsWarmedUp && m15.ATR > 0 && h1.EMA50 > 0 && h1.ATR > 0
	for _, v := range ok {
		pass = pass && v
	}
	sig := config.SignalHold
	if pass {
		sig = dir
	}
	if s.log != nil {
		b := func(k string) string {
			if ok[k] {
				return "1"
			}
			return "0"
		}
		e200 := "0"
		if h1.EMA200 > 0 && (buy && price > h1.EMA200 || !buy && price < h1.EMA200) {
			e200 = "1"
		}
		s.log.Write([]string{strconv.FormatInt(m5.BarTime+300, 10), dir, sig, b("h1_regime"), b("m5_ema"), b("m5_regime"), b("fresh"),
			b("adx"), b("ema50"), b("gap"), b("bounce"), b("cooldown"), fmt.Sprintf("%.1f", h1.ADX), fmt.Sprintf("%.2f", gapRatio),
			fmt.Sprintf("%.2f", bounceATR), fmt.Sprintf("%.2f", er), fmt.Sprintf("%.2f", atrx), e200, strconv.Itoa(hour)})
		s.log.Flush()
	}
	if !pass {
		return hold
	}
	atr := m15.ATR
	sl, tp := price-1.5*atr, price+2.5*atr
	if !buy {
		sl, tp = price+1.5*atr, price-2.5*atr
	}
	return strategy.EntryResult{Signal: dir, SLPrice: sl, TPPrice: tp, SLPips: 1.5 * atr / pipSize, TPPips: 2.5 * atr / pipSize,
		ATR: atr, Confluence: 1, Tier: config.TierNormal}
}

var eatLoc = time.FixedZone("EAT", 3*3600)
