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
	SkipReason          string // set when a BUY/SELL was recorded but deliberately not sent, e.g. "london_session"
	IntendedEntry       *float64
	IntendedSL          *float64
	IntendedTP          *float64
	CreatedAt           time.Time
}
