package main

import (
	"math"
	"os"

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
	n := reversalSignals(ms, p.side)
	return n >= 3 || n >= 2 && p.tier >= config.TierStronger
}
