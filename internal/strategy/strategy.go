package strategy

import (
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
)

type EntryResult struct {
	Signal       string
	Confluence   int
	Confidence   float64
	Tier         int
	SLPrice      float64
	TPPrice      float64
	SLPips       float64
	TPPips       float64
	ATR          float64
	StrategyName string
	Reason       string
	Rules        map[string]RuleCheck // entry checks as measured, saved to positions.decision_params
}

type RuleCheck struct {
	Value float64 `json:"value"`
	Limit float64 `json:"limit"`
	Why   string  `json:"why"`
}

type Strategy interface {
	Name() string

	Evaluate(states map[string]indicator.MarketState, currentPrice, pipSize float64) EntryResult

	UsesTrendWatcher() bool
}

type OutcomeAware interface {
	OnClosed(side, closeReason string, closeTime time.Time)
}

func ConfluenceToTier(c int) int {
	switch {
	case c >= 6:
		return config.TierVeryStrong
	case c >= 5:
		return config.TierStronger
	case c >= 4:
		return config.TierStrong
	default:
		return config.TierNormal
	}
}
