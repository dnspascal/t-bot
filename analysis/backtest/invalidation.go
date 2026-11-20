package main

import (
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
)

// Mirrors watcher.go's trend watcher: close when 3+ of regime/RSI/EMA-cross/momentum are against the trade
// (2+ for TierStronger and up). BT_INVAL=m5 checks on each M5 close like live; BT_INVAL=m5min re-evaluates
// the M5 indicators every minute against the forming bar; unset leaves the watcher off.
var invalMode = os.Getenv("BT_INVAL")

const rsiMidline = 50.0

// m5Live tracks what the M5 indicators would read if the forming bar closed now.
type m5Live struct {
	g, l, prevClose float64 // Wilder RSI(14) state up to the last closed M5 bar
	n               int
	ef, es          float64
	closes          []float64
	hist            []indicator.OHLC
	formHi, formLo  float64
}

func (m *m5Live) closed(ms indicator.MarketState, b bar) {
	if m.n > 0 {
		d := b.close - m.prevClose
		if m.n <= 14 {
			m.g += math.Max(d, 0) / 14
			m.l += math.Max(-d, 0) / 14
		} else {
			m.g = (m.g*13 + math.Max(d, 0)) / 14
			m.l = (m.l*13 + math.Max(-d, 0)) / 14
		}
	}
	m.n++
	m.prevClose = b.close
	m.ef, m.es = ms.EMAFast, ms.EMASlow
	m.closes = append(m.closes, b.close)
	m.hist = append(m.hist, indicator.OHLC{Open: b.open, High: b.high, Low: b.low, Close: b.close})
	if len(m.hist) > bufferSize {
		m.closes, m.hist = m.closes[1:], m.hist[1:]
	}
	m.formHi, m.formLo = 0, 0
}

func (m *m5Live) minute(b bar) {
	if m.formHi == 0 {
		m.formHi, m.formLo = b.high, b.low
		return
	}
	m.formHi, m.formLo = max(m.formHi, b.high), min(m.formLo, b.low)
}

// provisional returns the M5 state as if the forming bar closed at price c.
func (m *m5Live) provisional(c float64) indicator.MarketState {
	d := c - m.prevClose
	g, l := (m.g*13+math.Max(d, 0))/14, (m.l*13+math.Max(-d, 0))/14
	rsi := 100.0
	if l > 0 {
		rsi = 100 - 100/(1+g/l)
	}
	ef, es := m.ef+0.2*(c-m.ef), m.es+(2.0/22)*(c-m.es)
	hi, lo := c, c
	if m.formHi != 0 {
		hi, lo = max(m.formHi, c), min(m.formLo, c)
	}
	ohlc := append(append([]indicator.OHLC{}, m.hist...), indicator.OHLC{High: hi, Low: lo, Close: c})
	closes := append(append([]float64{}, m.closes...), c)
	return indicator.MarketState{Close: c, RSI: rsi, EMAFast: ef, EMASlow: es,
		Regime:            indicator.CalculateRegime(ef, es, 0, hi, lo, ohlc),
		MomentumDirection: indicator.CalculateMomentumDirection(rsi, closes)}
}

func reversalSignals(ms indicator.MarketState, side string) int {
	buy := side == config.SignalBuy
	n := 0
	if buy && (ms.Regime == config.TrendingDown || ms.Regime == config.Ranging) ||
		!buy && (ms.Regime == config.TrendingUp || ms.Regime == config.Ranging) {
		n++
	}
	if buy && ms.RSI < rsiMidline || !buy && ms.RSI > rsiMidline {
		n++
	}
	if buy && ms.EMAFast < ms.EMASlow || !buy && ms.EMAFast > ms.EMASlow {
		n++
	}
	if buy && ms.MomentumDirection == config.MomentumFalling || !buy && ms.MomentumDirection == config.MomentumRising {
		n++
	}
	return n
}

func invalidated(ms indicator.MarketState, p *position) bool {
	if invalRule == "rrm2" {
		return regimeRSIMomentum(ms, p.side) >= 2
	}
	if invalRule == "erm2" || invalRule == "erm3" {
		n := reversalSignals(ms, p.side) - regimeAgainst(ms, p.side)
		return n >= int(invalRule[3]-'0')
	}
	n := reversalSignals(ms, p.side)
	return n >= 3 || n >= 2 && p.tier >= config.TierStronger
}

// BT_INVAL_RULE=rrm2: close at 2 of regime, RSI, momentum; erm2/erm3: drop regime, close at 2 or 3 of EMA cross, RSI, momentum.
var invalRule = os.Getenv("BT_INVAL_RULE")

func regimeAgainst(ms indicator.MarketState, side string) int {
	if side == config.SignalBuy && (ms.Regime == config.TrendingDown || ms.Regime == config.Ranging) ||
		side != config.SignalBuy && (ms.Regime == config.TrendingUp || ms.Regime == config.Ranging) {
		return 1
	}
	return 0
}

func regimeRSIMomentum(ms indicator.MarketState, side string) int {
	buy := side == config.SignalBuy
	n := 0
	if buy && (ms.Regime == config.TrendingDown || ms.Regime == config.Ranging) ||
		!buy && (ms.Regime == config.TrendingUp || ms.Regime == config.Ranging) {
		n++
	}
	if buy && ms.RSI < rsiMidline || !buy && ms.RSI > rsiMidline {
		n++
	}
	if buy && ms.MomentumDirection == config.MomentumFalling || !buy && ms.MomentumDirection == config.MomentumRising {
		n++
	}
	return n
}

// BT_ENTRY_MAX_AGAINST=n skips entries when n or more of the four live reversal signals already oppose the side.
var entryMaxAgainst, _ = strconv.Atoi(os.Getenv("BT_ENTRY_MAX_AGAINST"))

func entryBlocked(m5 indicator.MarketState, side string) bool {
	return entryMaxAgainst > 0 && m5.IsWarmedUp && reversalSignals(m5, side) >= entryMaxAgainst
}

// BT_PD=off disables peak drawback; BT_LOCK="50:10,75:60,90:70" replaces breakeven with the live staged profit lock.
var (
	peakDrawbackOff = os.Getenv("BT_PD") == "off"
	lockSteps       = parseLock(os.Getenv("BT_LOCK"))
)

func parseLock(v string) [][2]float64 {
	var out [][2]float64
	for _, part := range strings.Split(v, ",") {
		a, b, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		reach, _ := strconv.ParseFloat(a, 64)
		lock, _ := strconv.ParseFloat(b, 64)
		out = append(out, [2]float64{reach, lock})
	}
	return out
}

// profitLock returns how far into profit the SL should sit for this peak, 0 if no step is reached.
func profitLock(peakGain, tpDist float64) float64 {
	lock := 0.0
	for _, st := range lockSteps {
		if peakGain >= tpDist*st[0]/100 {
			lock = max(tpDist*st[1]/100, breakEvenBufferPips*pipSize)
		}
	}
	return lock
}
