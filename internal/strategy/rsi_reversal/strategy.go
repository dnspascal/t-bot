package rsireversal

import (
	"math"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

const (
	rsiBuyThreshold  = 28.0 // H1 RSI below this → oversold → BUY
	rsiSellThreshold = 72.0 // H1 RSI above this → overbought → SELL
	slATRMult        = 1.0
	tpATRMult        = 1.5
)

type RSIReversal struct{}

func New() *RSIReversal { return &RSIReversal{} }

func (s *RSIReversal) Name() string           { return "rsi_reversal" }
func (s *RSIReversal) UsesTrendWatcher() bool { return true }

func (s *RSIReversal) Evaluate(states map[string]indicator.MarketState, currentPrice, pipSize float64) strategy.EntryResult {
	hold := func(rsn string) strategy.EntryResult {
		return strategy.EntryResult{Signal: config.SignalHold, Reason: rsn}
	}

	h1, ok := states[config.PeriodH1]
	if !ok || !h1.IsWarmedUp {
		return hold("H1 not warmed up")
	}

	var dir string
	switch {
	case h1.RSI < rsiBuyThreshold:
		dir = config.SignalBuy
	case h1.RSI > rsiSellThreshold:
		dir = config.SignalSell
	default:
		return hold("H1 RSI not at extreme")
	}

	// Skip when H1 is trending hard in the same direction — RSI can stay extreme in strong trends
	if dir == config.SignalBuy && h1.Regime == config.TrendingDown {
		return hold("H1 strongly trending down — RSI oversold may persist")
	}
	if dir == config.SignalSell && h1.Regime == config.TrendingUp {
		return hold("H1 strongly trending up — RSI overbought may persist")
	}

	// M5 confirmation: candle body must agree with reversal direction
	m5, ok := states[config.PeriodM5]
	if !ok || !m5.IsWarmedUp {
		return hold("M5 not warmed up")
	}
	if dir == config.SignalBuy && m5.Close <= m5.Open {
		return hold("M5 not showing bullish reversal candle")
	}
	if dir == config.SignalSell && m5.Close >= m5.Open {
		return hold("M5 not showing bearish reversal candle")
	}

	// H1 EMA50 filter: don't sell into an uptrend or buy into a downtrend
	if h1.EMA50 > 0 {
		if dir == config.SignalBuy && currentPrice > h1.EMA50 {
			return hold("BUY blocked — price above H1 EMA50 despite oversold RSI")
		}
		if dir == config.SignalSell && currentPrice < h1.EMA50 {
			return hold("SELL blocked — price below H1 EMA50 despite overbought RSI")
		}
	}

	m15, ok := states[config.PeriodM15]
	if !ok || !m15.IsWarmedUp || m15.ATR <= 0 {
		return hold("M15 ATR not ready")
	}

	atr := m15.ATR
	var slPrice, tpPrice float64
	if dir == config.SignalBuy {
		slPrice = currentPrice - slATRMult*atr
		tpPrice = currentPrice + tpATRMult*atr
	} else {
		slPrice = currentPrice + slATRMult*atr
		tpPrice = currentPrice - tpATRMult*atr
	}

	slPips := math.Abs(currentPrice-slPrice) / pipSize
	tpPips := math.Abs(tpPrice-currentPrice) / pipSize

	if slPips < 3 || tpPips < 3 {
		return hold("SL/TP too tight")
	}

	return strategy.EntryResult{
		Signal:  dir,
		SLPrice: slPrice,
		TPPrice: tpPrice,
		SLPips:  slPips,
		TPPips:  tpPips,
		ATR:     atr,
		Tier:    config.TierNormal,
	}
}
