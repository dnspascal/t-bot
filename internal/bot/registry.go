package bot

import (
	"sync"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
)

const (
	maxTotalPositions = 3
	maxPerStrategy    = 2 // a strategy can't pile up more than this many concurrent positions
	minScaleInPips    = 2 // existing position must be this many pips in profit before adding another
)

var maxPerTier = [4]int{4, 3, 2, 1}

type trackedPosition struct {
	ProviderPositionID string
	Side               string 
	Tier               int
	Volume             int64
	OpenPrice          float64
	SLPrice            float64
	TPPrice            float64
	ATR                float64
	OpenTime           time.Time
	MaxFavorable       float64 
	MaxAdverse         float64 
	StrategyName       string
	BreakEvenActive    bool // true once position reached 40% of SL distance favorable
}

type PositionRegistry struct {
	mu        sync.Mutex
	positions map[string]*trackedPosition
}

func newPositionRegistry() *PositionRegistry {
	return &PositionRegistry{positions: make(map[string]*trackedPosition)}
}

func (r *PositionRegistry) CanOpen(tier int, side, strategyName string, currentPrice, pipSize float64) (bool, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.positions) >= maxTotalPositions {
		return false, "max total positions reached"
	}
	if tier < 0 || tier >= len(maxPerTier) {
		return false, "invalid tier"
	}
	count := 0
	strategyCount := 0
	for _, p := range r.positions {
		if p.Side != side {
			return false, "conflicting direction — opposite position still open"
		}
		minDist := minScaleInPips * pipSize
		if p.Side == config.SignalBuy && currentPrice < p.OpenPrice+minDist {
			return false, "existing BUY not yet in profit"
		}
		if p.Side == config.SignalSell && currentPrice > p.OpenPrice-minDist {
			return false, "existing SELL not yet in profit"
		}
		if p.Tier == tier {
			count++
		}
		if p.StrategyName == strategyName {
			strategyCount++
		}
	}
	if count >= maxPerTier[tier] {
		return false, "max positions for tier reached"
	}
	if strategyCount >= maxPerStrategy {
		return false, "max positions for this strategy reached"
	}
	return true, ""
}

func (r *PositionRegistry) Register(pos trackedPosition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Seed peaks at open price so the first update has a valid baseline.
	if pos.MaxFavorable == 0 {
		pos.MaxFavorable = pos.OpenPrice
	}
	if pos.MaxAdverse == 0 {
		pos.MaxAdverse = pos.OpenPrice
	}
	r.positions[pos.ProviderPositionID] = &pos
}

func (r *PositionRegistry) SetBreakEven(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.positions[id]; ok {
		p.BreakEvenActive = true
	}
}

func (r *PositionRegistry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.positions, id)
}

func (r *PositionRegistry) Get(id string) (trackedPosition, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.positions[id]
	if !ok {
		return trackedPosition{}, false
	}
	return *p, true
}

func (r *PositionRegistry) All() []trackedPosition {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]trackedPosition, 0, len(r.positions))
	for _, p := range r.positions {
		out = append(out, *p)
	}
	return out
}

func (r *PositionRegistry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.positions)
}

func (r *PositionRegistry) UpdatePeaks(id string, currentPrice float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.positions[id]
	if !ok {
		return
	}
	if p.Side == config.SignalBuy {
		if currentPrice > p.MaxFavorable {
			p.MaxFavorable = currentPrice
		}
		if p.MaxAdverse == 0 || currentPrice < p.MaxAdverse {
			p.MaxAdverse = currentPrice
		}
	} else {
		if p.MaxFavorable == 0 || currentPrice < p.MaxFavorable {
			p.MaxFavorable = currentPrice
		}
		if currentPrice > p.MaxAdverse {
			p.MaxAdverse = currentPrice
		}
	}
}
