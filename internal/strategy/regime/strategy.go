// Package regime implements the multi-timeframe regime + range bounce strategy.
//
// # Logic overview
//
// Entry is evaluated on each M5 candle close. The strategy checks three
// scenarios in priority order:
//
//  1. HTF range bounce — M15+M30 both ranging, price near confirmed S/R,
//     RSI oversold/overbought (rsi<40 BUY, rsi>60 SELL). Bonus confluence
//     if H1/H4 also ranging. This is the highest-probability path (~46% win
//     rate in trending_down+ranging regime, 35-day backtest, 85 trades).
//
//  2. M5 trend follow — M5 trending_up or trending_down with RSI in the
//     middle band (not extreme). Requires H1 alignment and H1 ADX > 20.
//     Breakout variant also included but has only 10% win rate — kept to
//     accumulate data.
//
//  3. M5 ranging inside M15 trend — M5 consolidating inside an M15 trend;
//     treated as a continuation pullback.
//
// # Performance (35-day history, 85 trades, as at 2026-07)
//
//	regime       win%  trades
//	breakout      10%     21
//	ranging       20%     10
//	trending_up   25%      8
//	trending_down 46%     46  ← best path (HTF range bounce where M5 dips to S)
//
// # Known weaknesses
//
//   - CalculateRegime breakout detection uses wick (high/low) not close,
//     causing false breakout signals on any candle that pokes the range.
//   - EMA(9)/EMA(21) cross on M5 is lagging — by cross time the move is done.
//
// STRATEGY env value: "regime"
package regime

import (
	"slices"
	"time"

	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

const (
	rsiMidline                 = 50.0
	rsiOversold                = 40.0
	rsiOverbought              = 60.0
	srProximityMult            = 1.5
	srAlignmentPips            = 7.0
	slATRMult                  = 1.5
	tpATRMult                  = 2.5
	slRangeBuffer              = 0.25
	tpRangeBuffer              = 0.15
	minRR                      = 1.5
	minATRPips                 = 3.0
	minH1ADXTrend              = 20.0
	consecutiveFailsToCooldown = 2
	cooldownDuration           = 30 * time.Minute
)

var confluenceTimeframes = []string{"M15", "M30", "H1", "H4"}
var rangeRequiredTFs = []string{"M15", "M30"}
var rangeBonusTFs = []string{"H1", "H4"}

type rangeConfirmation struct {
	confirmed           bool
	reason              string
	consensusSupport    float64
	consensusResistance float64
	farSupport          float64
	farResistance       float64
	bonusTFs            int
}

// Regime is the multi-timeframe regime strategy.
type Regime struct {
	buyFailStreak  int
	sellFailStreak int
	cooldownDir    string
	cooldownUntil  time.Time
}

func New() *Regime { return &Regime{} }

func (r *Regime) Name() string           { return "regime" }
func (r *Regime) UsesTrendWatcher() bool { return true }

// OnClosed implements strategy.OutcomeAware.
func (r *Regime) OnClosed(side, closeReason string, closeTime time.Time) {
	switch strategy.ClassifyCloseReason(closeReason) {
	case strategy.CloseInvalidated:
		if side == "BUY" {
			r.buyFailStreak++
			if r.buyFailStreak >= consecutiveFailsToCooldown {
				r.cooldownDir = "BUY"
				r.cooldownUntil = closeTime.Add(cooldownDuration)
			}
		} else {
			r.sellFailStreak++
			if r.sellFailStreak >= consecutiveFailsToCooldown {
				r.cooldownDir = "SELL"
				r.cooldownUntil = closeTime.Add(cooldownDuration)
			}
		}
	case strategy.CloseValidated:
		if side == "BUY" {
			r.buyFailStreak = 0
		} else {
			r.sellFailStreak = 0
		}
		if r.cooldownDir == side {
			r.cooldownDir = ""
		}
	case strategy.CloseNeutral:
		// Doesn't say anything about the setup either way — leave the streak alone.
	}
}

func (r *Regime) Evaluate(states map[string]indicator.MarketState, currentPrice float64, pipSize float64) strategy.EntryResult {
	hold := func(reason string) strategy.EntryResult {
		return strategy.EntryResult{Signal: "HOLD", Reason: reason}
	}

	m5, ok := states["M5"]
	if !ok || !m5.IsWarmedUp {
		return hold("M5 not warmed up")
	}

	var direction string
	var isRanging bool
	var rangeConf rangeConfirmation
	var rangeATR = m5.ATR

	if m5.Regime != "ranging" {
		if htfConf, htfAnchor, ok := confirmHigherTFRange(states, pipSize); ok {
			dir := rangingDirection(htfAnchor, htfConf, currentPrice)
			if dir == "" {
				return hold("M15+M30 ranging: price not near S/R or RSI not confirming")
			}
			direction = dir
			rangeConf = htfConf
			isRanging = true
			rangeATR = htfAnchor.ATR
		}
	}

	if !isRanging {
		var tentativeDir string
		switch {
		case m5.Regime == "trending_up":
			tentativeDir = "BUY"
		case m5.Regime == "trending_down":
			tentativeDir = "SELL"
		case m5.Regime == "breakout":
			if m5.EMAFast > m5.EMASlow {
				tentativeDir = "BUY"
			} else {
				tentativeDir = "SELL"
			}
		}

		if tentativeDir != "" {
			for _, tf := range []string{"M15", "M30"} {
				s, ok := states[tf]
				if !ok || !s.IsWarmedUp || s.Regime != "ranging" {
					continue
				}
				h1, h1ok := states["H1"]
				if !h1ok || !h1.IsWarmedUp || h1.Regime == "ranging" {
					return hold(tf + " ranging — M5 trend is noise inside higher range")
				}
				if tentativeDir == "SELL" && h1.Regime != "trending_down" {
					return hold(tf + " ranging — H1 not confirming SELL")
				}
				if tentativeDir == "BUY" && h1.Regime != "trending_up" {
					return hold(tf + " ranging — H1 not confirming BUY")
				}
			}
		}

		if tentativeDir != "" {
			if h1, ok := states["H1"]; ok && h1.IsWarmedUp && h1.ADX < minH1ADXTrend {
				return hold("H1 ADX too weak — no trend conviction")
			}
		}

		switch {
		case m5.Regime == "trending_up" && m5.RSI > rsiMidline && m5.RSI < rsiOverbought:
			direction = "BUY"
		case m5.Regime == "trending_down" && m5.RSI < rsiMidline && m5.RSI > rsiOversold:
			direction = "SELL"

		case m5.Regime == "breakout" && m5.EMAFast > m5.EMASlow && m5.RSI > rsiMidline && m5.RSI < rsiOverbought:
			direction = "BUY"
		case m5.Regime == "breakout" && m5.EMAFast < m5.EMASlow && m5.RSI < rsiMidline && m5.RSI > rsiOversold:
			direction = "SELL"

		case m5.Regime == "ranging":
			m15State, m15ok := states["M15"]
			if m15ok && m15State.IsWarmedUp && (m15State.Regime == "trending_up" || m15State.Regime == "trending_down") {
				if m5.SupportLevel <= 0 || m5.ResistanceLevel <= 0 || m5.ATR <= 0 {
					return hold("ranging: M5 S/R not established")
				}
				m5Conf := rangeConfirmation{
					confirmed:           true,
					consensusSupport:    m5.SupportLevel,
					consensusResistance: m5.ResistanceLevel,
					farSupport:          m5.SupportLevel,
					farResistance:       m5.ResistanceLevel,
					bonusTFs:            1,
				}
				dir := rangingDirection(m5, m5Conf, currentPrice)
				if dir == "" {
					return hold("ranging: price not near M5 S/R or RSI not confirming")
				}
				if m15State.Regime == "trending_up" && dir != "BUY" {
					return hold("ranging: M5 at resistance but M15 trending up — skip counter-trend")
				}
				if m15State.Regime == "trending_down" && dir != "SELL" {
					return hold("ranging: M5 at support but M15 trending down — skip counter-trend")
				}
				direction = dir
				rangeConf = m5Conf
				isRanging = true
			} else {
				rangeConf = confirmRange(m5, states, pipSize)
				if !rangeConf.confirmed {
					return hold("ranging: " + rangeConf.reason)
				}
				direction = rangingDirection(m5, rangeConf, currentPrice)
				if direction == "" {
					return hold("ranging: price not near confirmed S/R or RSI not confirming")
				}
				isRanging = true
			}

		default:
			return hold("no M5 trend trigger")
		}
	}

	if direction == r.cooldownDir && !r.cooldownUntil.IsZero() {
		now := time.Unix(m5.BarTime, 0)
		if now.Before(r.cooldownUntil) {
			return hold("cooling down after repeated same-direction invalidation")
		}
	}

	if isRanging {
		if m15, ok := states["M15"]; ok && m15.ATR/pipSize < minATRPips {
			return hold("M15 ATR too small for ranging SL/TP")
		}
	} else {
		if m5.ATR/pipSize < minATRPips {
			return hold("M5 ATR too small — spread would eat SL")
		}
	}

	confluence := 1

	if isRanging {
		confluence += rangeConf.bonusTFs
	} else {
		for _, tf := range confluenceTimeframes {
			s, ok := states[tf]
			if !ok || !s.IsWarmedUp {
				continue
			}
			if direction == "BUY" && s.Regime == "trending_up" {
				confluence++
			} else if direction == "SELL" && s.Regime == "trending_down" {
				confluence++
			}
		}
		if direction == "BUY" && m5.SupportLevel > 0 && (currentPrice-m5.SupportLevel) <= srProximityMult*m5.ATR {
			confluence++
		}
		if direction == "SELL" && m5.ResistanceLevel > 0 && (m5.ResistanceLevel-currentPrice) <= srProximityMult*m5.ATR {
			confluence++
		}
	}

	if m5.Volume > 0 && m5.VolumeMA > 0 && m5.Volume > m5.VolumeMA {
		confluence++
	}

	if confluence < 2 {
		return strategy.EntryResult{Signal: "HOLD", Confluence: confluence, Reason: "insufficient confluence"}
	}

	var slPrice, tpPrice float64
	if isRanging {
		slPrice, tpPrice = computeRangeSLTP(rangeConf, direction, currentPrice, rangeATR)
	} else {
		slPrice, tpPrice = computeSLTP(m5, direction, currentPrice)
	}

	if slPrice <= 0 || tpPrice <= 0 {
		return hold("cannot compute valid SL/TP")
	}

	slPips, tpPips := pricesToPips(direction, currentPrice, slPrice, tpPrice, pipSize)
	if slPips < 3 || tpPips < 3 {
		return hold("SL/TP too tight in pips")
	}
	if tpPips/slPips < minRR {
		return hold("risk/reward too low")
	}

	return strategy.EntryResult{
		Signal:     direction,
		Confluence: confluence,
		Confidence: computeConfidence(m5, states, direction, slPips, tpPips),
		Tier:       strategy.ConfluenceToTier(confluence),
		SLPrice:    slPrice,
		TPPrice:    tpPrice,
		SLPips:     slPips,
		TPPips:     tpPips,
		ATR:        m5.ATR,
	}
}

func computeConfidence(m5 indicator.MarketState, states map[string]indicator.MarketState, direction string, slPips, tpPips float64) float64 {
	var score float64

	rsiDev := m5.RSI - rsiMidline
	if direction == "SELL" {
		rsiDev = rsiMidline - m5.RSI
	}
	if rsiDev < 0 {
		rsiDev = 0
	}
	score += min(rsiDev/10.0, 1.0) * 25

	if m5.ADX > 0 {
		score += min((m5.ADX-20)/20.0, 1.0) * 25
	}

	if m5.ATR > 0 {
		spread := m5.EMAFast - m5.EMASlow
		if direction == "SELL" {
			spread = m5.EMASlow - m5.EMAFast
		}
		score += min(spread/(m5.ATR*2), 1.0) * 20
	}

	for _, tf := range confluenceTimeframes {
		s, ok := states[tf]
		if !ok || !s.IsWarmedUp {
			continue
		}
		if direction == "BUY" && s.Regime == "trending_up" {
			score += 5
		} else if direction == "SELL" && s.Regime == "trending_down" {
			score += 5
		}
	}

	rr := tpPips / slPips
	score += min((rr-minRR)/minRR, 1.0) * 10

	return min(score/100.0, 1.0)
}

func confirmRange(m5 indicator.MarketState, states map[string]indicator.MarketState, pipSize float64) rangeConfirmation {
	fail := func(r string) rangeConfirmation { return rangeConfirmation{reason: r} }

	if m5.SupportLevel <= 0 || m5.ResistanceLevel <= 0 || m5.ATR <= 0 {
		return fail("M5 S/R not established")
	}

	supports := []float64{m5.SupportLevel}
	resistances := []float64{m5.ResistanceLevel}

	for _, tf := range rangeRequiredTFs {
		s, ok := states[tf]
		if !ok || !s.IsWarmedUp || s.Regime != "ranging" {
			return fail(tf + " not ranging")
		}
		if s.SupportLevel <= 0 || s.ResistanceLevel <= 0 {
			return fail(tf + " missing S/R levels")
		}
		supports = append(supports, s.SupportLevel)
		resistances = append(resistances, s.ResistanceLevel)
	}

	bonusTFs := 0
	for _, tf := range rangeBonusTFs {
		s, ok := states[tf]
		if !ok || !s.IsWarmedUp || s.Regime != "ranging" {
			continue
		}
		if s.SupportLevel <= 0 || s.ResistanceLevel <= 0 {
			continue
		}
		supports = append(supports, s.SupportLevel)
		resistances = append(resistances, s.ResistanceLevel)
		bonusTFs++
	}

	tolerance := srAlignmentPips * pipSize
	if slices.Max(supports)-slices.Min(supports) > tolerance {
		return fail("support levels misaligned across timeframes")
	}
	if slices.Max(resistances)-slices.Min(resistances) > tolerance {
		return fail("resistance levels misaligned across timeframes")
	}

	consensusSupport := slices.Max(supports)
	consensusResistance := slices.Min(resistances)

	if consensusResistance <= consensusSupport {
		return fail("range collapsed — resistance below support")
	}

	return rangeConfirmation{
		confirmed:           true,
		consensusSupport:    consensusSupport,
		consensusResistance: consensusResistance,
		farSupport:          slices.Min(supports),
		farResistance:       slices.Max(resistances),
		bonusTFs:            bonusTFs,
	}
}

func confirmHigherTFRange(states map[string]indicator.MarketState, pipSize float64) (rangeConfirmation, indicator.MarketState, bool) {
	m15, ok := states["M15"]
	if !ok || !m15.IsWarmedUp || m15.Regime != "ranging" || m15.SupportLevel <= 0 || m15.ResistanceLevel <= 0 || m15.ATR <= 0 {
		return rangeConfirmation{}, indicator.MarketState{}, false
	}
	m30, ok := states["M30"]
	if !ok || !m30.IsWarmedUp || m30.Regime != "ranging" || m30.SupportLevel <= 0 || m30.ResistanceLevel <= 0 {
		return rangeConfirmation{}, indicator.MarketState{}, false
	}

	supports := []float64{m15.SupportLevel, m30.SupportLevel}
	resistances := []float64{m15.ResistanceLevel, m30.ResistanceLevel}

	bonusTFs := 0
	for _, tf := range rangeBonusTFs {
		s, ok := states[tf]
		if !ok || !s.IsWarmedUp || s.Regime != "ranging" || s.SupportLevel <= 0 || s.ResistanceLevel <= 0 {
			continue
		}
		supports = append(supports, s.SupportLevel)
		resistances = append(resistances, s.ResistanceLevel)
		bonusTFs++
	}

	tolerance := srAlignmentPips * pipSize
	if slices.Max(supports)-slices.Min(supports) > tolerance {
		return rangeConfirmation{}, indicator.MarketState{}, false
	}
	if slices.Max(resistances)-slices.Min(resistances) > tolerance {
		return rangeConfirmation{}, indicator.MarketState{}, false
	}

	consensusSupport := slices.Max(supports)
	consensusResistance := slices.Min(resistances)
	if consensusResistance <= consensusSupport {
		return rangeConfirmation{}, indicator.MarketState{}, false
	}

	return rangeConfirmation{
		confirmed:           true,
		consensusSupport:    consensusSupport,
		consensusResistance: consensusResistance,
		farSupport:          slices.Min(supports),
		farResistance:       slices.Max(resistances),
		bonusTFs:            bonusTFs,
	}, m15, true
}

func rangingDirection(m5 indicator.MarketState, rc rangeConfirmation, price float64) string {
	nearSupport := price > rc.consensusSupport && (price-rc.consensusSupport) <= srProximityMult*m5.ATR
	nearResistance := price < rc.consensusResistance && (rc.consensusResistance-price) <= srProximityMult*m5.ATR

	if nearSupport && m5.RSI < rsiOversold {
		return "BUY"
	}
	if nearResistance && m5.RSI > rsiOverbought {
		return "SELL"
	}
	return ""
}

func computeRangeSLTP(rc rangeConfirmation, direction string, price, atr float64) (slPrice, tpPrice float64) {
	if direction == "BUY" {
		sl := rc.farSupport - slRangeBuffer*atr
		tp := rc.consensusResistance - tpRangeBuffer*atr
		if sl < price && tp > price {
			return sl, tp
		}
	} else {
		sl := rc.farResistance + slRangeBuffer*atr
		tp := rc.consensusSupport + tpRangeBuffer*atr
		if sl > price && tp < price {
			return sl, tp
		}
	}
	return 0, 0
}

func computeSLTP(m5 indicator.MarketState, direction string, price float64) (slPrice, tpPrice float64) {
	atr := m5.ATR
	if atr <= 0 {
		return 0, 0
	}
	if direction == "BUY" {
		return price - slATRMult*atr, price + tpATRMult*atr
	}
	return price + slATRMult*atr, price - tpATRMult*atr
}

func pricesToPips(direction string, entry, slPrice, tpPrice, pipSize float64) (slPips, tpPips float64) {
	if direction == "BUY" {
		slPips = (entry - slPrice) / pipSize
		tpPips = (tpPrice - entry) / pipSize
	} else {
		slPips = (slPrice - entry) / pipSize
		tpPips = (entry - tpPrice) / pipSize
	}
	return
}
