package marketstate

import (
	"context"
	"log/slog"
	"time"

	"github.com/denismgaya/t-bot/internal/indicator"
)

type Processor struct {
	symbolID   string
	provider   string
	period     string
	buffer     CandleBuffer
	calculator *indicator.Calculator
	repo       Repository
	lastID     string

	// Pre-session high/low accumulators (London and NY)
	londonPreHigh float64
	londonPreLow  float64
	nyPreHigh     float64
	nyPreLow      float64
}

func NewProcessor(
	symbolID, provider, period string,
	buffer CandleBuffer,
	repo Repository,
) *Processor {
	return &Processor{
		symbolID:   symbolID,
		provider:   provider,
		period:     period,
		buffer:     buffer,
		calculator: indicator.NewCalculator(),
		repo:       repo,
	}
}

func (p *Processor) WarmCandle(openTime int64, open, high, low, close float64, volume int64) {
	historical := p.buffer.Closes()
	historicalVolumes := p.buffer.Volumes()
	historicalOHLC := p.buffer.OHLC()
	p.buffer.AddCandle(open, high, low, close, volume)
	p.calculator.Calculate(
		p.symbolID, p.provider, p.period,
		openTime,
		open, high, low, close,
		volume,
		historical,
		historicalVolumes,
		historicalOHLC,
	)
	p.accumulateSessionRange(openTime, high, low)
}

// accumulateSessionRange updates the pre-session high/low accumulators.
// Called on every candle so the accumulators are ready when session opens.
func (p *Processor) accumulateSessionRange(openTimeMs int64, high, low float64) {
	barUTC := time.Unix(openTimeMs, 0).UTC()
	h, m := barUTC.Hour(), barUTC.Minute()

	switch {
	case h == 6: // pre-London accumulation window
		if p.londonPreHigh == 0 || high > p.londonPreHigh {
			p.londonPreHigh = high
		}
		if p.londonPreLow == 0 || low < p.londonPreLow {
			p.londonPreLow = low
		}
	case h == 7 && m >= 30: // London session window closed — reset for next day
		p.londonPreHigh = 0
		p.londonPreLow = 0
	case h == 12: // pre-NY accumulation window
		if p.nyPreHigh == 0 || high > p.nyPreHigh {
			p.nyPreHigh = high
		}
		if p.nyPreLow == 0 || low < p.nyPreLow {
			p.nyPreLow = low
		}
	case h == 13 && m >= 30: // NY session window closed — reset for next day
		p.nyPreHigh = 0
		p.nyPreLow = 0
	}
}

// injectSessionRange sets SessionHigh/Low on the market state if we are inside a session open window.
func (p *Processor) injectSessionRange(openTimeMs int64, ms *indicator.MarketState) {
	barUTC := time.Unix(openTimeMs, 0).UTC()
	h, m := barUTC.Hour(), barUTC.Minute()

	if h == 7 && m < 30 && p.londonPreHigh > 0 {
		ms.SessionHigh = p.londonPreHigh
		ms.SessionLow = p.londonPreLow
	} else if h == 13 && m < 30 && p.nyPreHigh > 0 {
		ms.SessionHigh = p.nyPreHigh
		ms.SessionLow = p.nyPreLow
	}
}

func (p *Processor) Commit(ctx context.Context) error {
	id, err := p.repo.Insert(ctx, p.calculator.LastState())
	if err == nil {
		p.lastID = id
	}
	return err
}

func (p *Processor) ProcessCandle(ctx context.Context, openTime int64, open, high, low, close float64, volume int64, receivedAt time.Time) (indicator.MarketState, error) {
	historical := p.buffer.Closes()
	historicalVolumes := p.buffer.Volumes()
	historicalOHLC := p.buffer.OHLC()
	p.buffer.AddCandle(open, high, low, close, volume)

	marketState := p.calculator.Calculate(
		p.symbolID, p.provider, p.period,
		openTime,
		open, high, low, close,
		volume,
		historical,
		historicalVolumes,
		historicalOHLC,
	)

	p.accumulateSessionRange(openTime, high, low)
	p.injectSessionRange(openTime, &marketState)

	marketState.ProcessingUS = time.Since(receivedAt).Microseconds()

	id, err := p.repo.Insert(ctx, marketState)
	if err != nil {
		slog.Error("failed to store market state", "period", p.period, "symbolID", p.symbolID, "err", err)
		return marketState, err
	}
	p.lastID = id
	marketState.ID = id

	return marketState, nil
}

func (p *Processor) State() indicator.MarketState {
	s := p.calculator.LastState()
	s.ID = p.lastID
	return s
}

// IsWarmedUp returns true if this processor has enough data for all indicators.
func (p *Processor) IsWarmedUp() bool {
	return p.buffer.IsWarmedUp()
}

// ProcessorManager manages processors for all timeframes of one symbol.
type ProcessorManager struct {
	processors  map[string]*Processor
	symbolID    string
	provider    string
	repo        Repository
	warmupSkips map[string]bool
}

func NewProcessorManager(symbolID, provider string, repo Repository) *ProcessorManager {
	return &ProcessorManager{
		processors:  make(map[string]*Processor),
		symbolID:    symbolID,
		provider:    provider,
		repo:        repo,
		warmupSkips: map[string]bool{"M1": true}, // M1 is watcher-only, always skipped
	}
}

// SkipWarmup marks additional timeframes as non-blocking for AllWarmedUp.
// Used in dev mode to skip H4/D1 when testnet has insufficient historical data.
func (m *ProcessorManager) SkipWarmup(periods ...string) {
	for _, p := range periods {
		m.warmupSkips[p] = true
	}
}

func (m *ProcessorManager) AddProcessor(period string, processor *Processor) {
	m.processors[period] = processor
}

// WarmCandle advances a timeframe's state without writing to DB.
func (m *ProcessorManager) WarmCandle(period string, openTime int64, open, high, low, close float64, volume int64) {
	if p, ok := m.processors[period]; ok {
		p.WarmCandle(openTime, open, high, low, close, volume)
	}
}

// CommitWarmup inserts the final warm-up state for a timeframe — one row per timeframe.
func (m *ProcessorManager) CommitWarmup(ctx context.Context, period string) error {
	p, ok := m.processors[period]
	if !ok {
		return nil
	}
	return p.Commit(ctx)
}

func (m *ProcessorManager) ProcessCandle(ctx context.Context, period string, openTime int64, open, high, low, close float64, volume int64, receivedAt time.Time) (map[string]indicator.MarketState, error) {
	results := make(map[string]indicator.MarketState)

	processor, ok := m.processors[period]
	if !ok {
		return results, nil
	}

	state, err := processor.ProcessCandle(ctx, openTime, open, high, low, close, volume, receivedAt)
	if err != nil {
		return results, err
	}

	results[period] = state
	return results, nil
}

func (m *ProcessorManager) GetAllStates() map[string]indicator.MarketState {
	states := make(map[string]indicator.MarketState)
	for period, processor := range m.processors {
		states[period] = processor.State()
	}
	return states
}

func (m *ProcessorManager) AllWarmedUp() bool {
	for period, processor := range m.processors {
		if m.warmupSkips[period] {
			continue
		}
		if !processor.IsWarmedUp() {
			return false
		}
	}
	return len(m.processors) > 0
}
