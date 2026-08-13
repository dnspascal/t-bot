package sessionmomentum

import (
	"fmt"
	"math"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/strategy"
)

const (
	slATRMult                  = 1.0
	tpATRMult                  = 2.0
	minBodyATRRatio            = 0.4
	consecutiveFailsToCooldown = 2
	cooldownDuration           = 30 * time.Minute
)

type SessionMomentum struct {
	lastTradeSession string
	buyFailStreak    int
	sellFailStreak   int
	cooldownDir      string
	cooldownUntil    time.Time
}

func New() *SessionMomentum { return &SessionMomentum{} }

func (s *SessionMomentum) Name() string           { return "session_momentum" }
func (s *SessionMomentum) UsesTrendWatcher() bool { return false }

// OnClosed implements strategy.OutcomeAware.
func (s *SessionMomentum) OnClosed(side, closeReason string, closeTime time.Time) {
	switch strategy.ClassifyCloseReason(closeReason) {
	case strategy.CloseInvalidated:
		if side == config.SignalBuy {
			s.buyFailStreak++
			if s.buyFailStreak >= consecutiveFailsToCooldown {
				s.cooldownDir = config.SignalBuy
				s.cooldownUntil = closeTime.Add(cooldownDuration)
			}
		} else {
			s.sellFailStreak++
			if s.sellFailStreak >= consecutiveFailsToCooldown {
				s.cooldownDir = config.SignalSell
				s.cooldownUntil = closeTime.Add(cooldownDuration)
			}
		}
	case strategy.CloseValidated:
		if side == config.SignalBuy {
			s.buyFailStreak = 0
		} else {
			s.sellFailStreak = 0
		}
		if s.cooldownDir == side {
			s.cooldownDir = ""
		}
	case strategy.CloseNeutral:
		// Doesn't say anything about the setup either way — leave the streak alone.
	}
}

func (s *SessionMomentum) Evaluate(states map[string]indicator.MarketState, currentPrice, pipSize float64) strategy.EntryResult {
	hold := func(rsn string) strategy.EntryResult {
		return strategy.EntryResult{Signal: config.SignalHold, Reason: rsn}
	}

	m5, ok := states[config.PeriodM5]
	if !ok || !m5.IsWarmedUp || m5.ATR <= 0 {
		return hold("M5 not warmed up")
	}

	barTime := time.Unix(m5.BarTime, 0).UTC()
	h, min := barTime.Hour(), barTime.Minute()
	dateStr := barTime.Format("2006-01-02")

	var sessionKey string
	switch {
	case h == 7 && min < 30:
		sessionKey = fmt.Sprintf("london-%s", dateStr)
	case h == 13 && min < 30:
		sessionKey = fmt.Sprintf("ny-%s", dateStr)
	default:
		return hold("not in session open window (London 07:00-07:30 or NY 13:00-13:30 UTC)")
	}

	if s.lastTradeSession == sessionKey {
		return hold("already traded this session window")
	}

	body := m5.Close - m5.Open
	absBody := math.Abs(body)
	if absBody < minBodyATRRatio*m5.ATR {
		return hold("M5 candle body too small — no clear session momentum")
	}

	var dir string
	if body > 0 {
		dir = config.SignalBuy
	} else {
		dir = config.SignalSell
	}

	if dir == s.cooldownDir && !s.cooldownUntil.IsZero() {
		if barTime.Before(s.cooldownUntil) {
			return hold("cooling down after repeated same-direction invalidation")
		}
	}

	if h1, ok := states[config.PeriodH1]; ok && h1.IsWarmedUp && h1.EMA50 > 0 {
		if dir == config.SignalBuy && currentPrice < h1.EMA50 {
			return hold("BUY session momentum blocked — price below H1 EMA50")
		}
		if dir == config.SignalSell && currentPrice > h1.EMA50 {
			return hold("SELL session momentum blocked — price above H1 EMA50")
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

	s.lastTradeSession = sessionKey

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
