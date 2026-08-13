package emapullback

import (
	"math"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

const (
	slATRMult = 1.0
	emaProximityATRs = 1.0
	adxTrendFloor = 25.0
)

type EMAPullback struct {
	lastEntryH1BarTime int64
}

func New() *EMAPullback { return &EMAPullback{} }

func (s *EMAPullback) Name() string           { return "ema_pullback" }
func (s *EMAPullback) UsesTrendWatcher() bool { return true }

func (s *EMAPullback) Evaluate(states map[string]indicator.MarketState, currentPrice, pipSize float64) strategy.EntryResult {
	hold := func(rsn string) strategy.EntryResult {
		return strategy.EntryResult{Signal: config.SignalHold, Reason: rsn}
	}

	h1, ok := states[config.PeriodH1]
	if !ok || !h1.IsWarmedUp {
		return hold("H1 not warmed up")
	}

	if h1.BarTime == s.lastEntryH1BarTime {
		return hold("ema_pullback already signaled this H1 bar")
	}

	if h1.ADX < adxTrendFloor {
		return hold("H1 ADX too weak — regime label isn't backed by a real trend")
	}

	var dir string
	switch h1.Regime {
	case config.TrendingUp:
		dir = config.SignalBuy
	case config.TrendingDown:
		dir = config.SignalSell
	default:
		return hold("H1 not trending — no pullback to trade")
	}

	m5, ok := states[config.PeriodM5]
	if !ok || !m5.IsWarmedUp || m5.ATR <= 0 {
		return hold("M5 not warmed up")
	}

	emaProximity := emaProximityATRs * m5.ATR
	distanceFromEMA := math.Abs(currentPrice - h1.EMASlow)
	if distanceFromEMA > emaProximity {
		return hold("price not close enough to H1 EMA21 for pullback entry")
	}


	if dir == config.SignalBuy && currentPrice > h1.EMASlow+emaProximity {
		return hold("price still too far above EMA — not a pullback")
	}
	if dir == config.SignalSell && currentPrice < h1.EMASlow-emaProximity {
		return hold("price still too far below EMA — not a pullback")
	}

	if dir == config.SignalBuy && m5.Close <= m5.Open {
		return hold("M5 not yet showing bullish bounce off EMA")
	}
	if dir == config.SignalSell && m5.Close >= m5.Open {
		return hold("M5 not yet showing bearish bounce off EMA")
	}

	m15, ok := states[config.PeriodM15]
	if !ok || !m15.IsWarmedUp || m15.ATR <= 0 {
		return hold("M15 ATR not ready")
	}

	atr := m15.ATR

	var slPrice, tpPrice float64
	if dir == config.SignalBuy {
		slPrice = h1.EMASlow - slATRMult*atr
		tpPrice = h1.TrendHigh
	} else {
		slPrice = h1.EMASlow + slATRMult*atr
		tpPrice = h1.TrendLow
	}

	if dir == config.SignalBuy && tpPrice <= currentPrice {
		return hold("H1 TrendHigh is at or below current price")
	}
	if dir == config.SignalSell && tpPrice >= currentPrice {
		return hold("H1 TrendLow is at or above current price")
	}

	slPips := math.Abs(currentPrice-slPrice) / pipSize
	tpPips := math.Abs(tpPrice-currentPrice) / pipSize

	if slPips < 3 || tpPips < 3 {
		return hold("SL/TP too tight")
	}

	// Require at least 1:1 R:R
	if tpPips < slPips {
		return hold("R:R below 1:1 — pullback entry not favourable")
	}

	s.lastEntryH1BarTime = h1.BarTime

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
