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

// strategyMaxOpen overrides maxPerStrategy for strategies that must not stack positions.
var strategyMaxOpen = map[string]int{"trend_pullback": 1, "trend_pullback_nogap": 1}

func maxOpenFor(strategyName string) int {
	if n, ok := strategyMaxOpen[strategyName]; ok {
		return n
	}
	return maxPerStrategy
}

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
	BreakEvenActive    bool // true once the broker has CONFIRMED the break-even SL amendment
	BreakEvenPending   bool // true while an amendment has been sent but not yet confirmed or timed out
	BreakEvenAttempts  int  // number of amend attempts sent, so we can give up after breakEvenMaxAttempts
	BreakEvenGaveUp    bool // true once we've stopped retrying an unconfirmed amendment
	LockStage          int  // highest profit-lock step the broker has confirmed (0 = none)
	AttemptStage       int  // profit-lock step BreakEvenAttempts counts for
	GaveUpStage        int  // profit-lock step that was given up on; a higher step is still tried
	PeakDrawbackShadow bool // true once the disabled peak drawback would have closed this position
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
	if strategyCount >= maxOpenFor(strategyName) {
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

// SetLockConfirmed marks a profit-lock step as CONFIRMED by the broker and records the new SL.
func (r *PositionRegistry) SetLockConfirmed(id string, stage int, sl float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.positions[id]; ok {
		p.BreakEvenActive = true
		p.BreakEvenPending = false
		p.LockStage = max(p.LockStage, stage)
		p.SLPrice = sl
	}
}

// SetBreakEvenPending marks that an amendment was sent and is awaiting broker
// confirmation, so checkBreakEven doesn't fire a duplicate amend on the next tick.
func (r *PositionRegistry) SetBreakEvenPending(id string, pending bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.positions[id]; ok {
		p.BreakEvenPending = pending
	}
}

// IncrementBreakEvenAttempts records another amend attempt for a profit-lock step and returns its count.
func (r *PositionRegistry) IncrementBreakEvenAttempts(id string, stage int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.positions[id]; ok {
		if p.AttemptStage != stage {
			p.AttemptStage, p.BreakEvenAttempts = stage, 0
		}
		p.BreakEvenAttempts++
		return p.BreakEvenAttempts
	}
	return 0
}

// SetBreakEvenGaveUp stops amend retries for this profit-lock step; a higher step is still tried.
func (r *PositionRegistry) SetBreakEvenGaveUp(id string, stage int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.positions[id]; ok {
		p.BreakEvenGaveUp = true
		p.GaveUpStage = max(p.GaveUpStage, stage)
		p.BreakEvenPending = false
	}
}

// MarkPeakDrawbackShadow flags the position and reports whether this is the first time.
func (r *PositionRegistry) MarkPeakDrawbackShadow(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.positions[id]
	if !ok || p.PeakDrawbackShadow {
		return false
	}
	p.PeakDrawbackShadow = true
	return true
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
