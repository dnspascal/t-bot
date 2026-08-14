package signal

import "time"

// MarketStateSnapshot references the market_states row used when evaluating a signal.
// Full indicator values live in market_states — join on id to retrieve them.
type MarketStateSnapshot struct {
	MarketStateID string `json:"id"`
}

type Signal struct {
	ID                  string
	SymbolID            string
	Provider            string
	Signal              string // BUY | SELL | HOLD
	Reason              string // why this signal was generated or held
	Confluence          int
	Confidence          float64 // 0.0–1.0 weighted factor strength
	ProcessingUS        int64
	CheckedMarketStates map[string]MarketStateSnapshot // keyed by period: "M5", "H1", etc.
	BarTime             *time.Time
	Strategy            string // which strategy produced this signal: "regime", "sr_bounce", etc.
	CreatedAt           time.Time
}
