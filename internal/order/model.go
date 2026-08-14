package order

import "time"

type Order struct {
	ID                  string
	SignalID            *string
	Provider            string
	ProviderOrderID     *string
	ProviderPositionID  *string
	SymbolID            string
	Side                string // BUY | SELL
	Volume              int64
	SL                  *float64
	TP                  *float64
	EntryPrice          *float64
	SlippagePoints      *int64
	Status              string // pending | accepted | filled | partially_filled | rejected | cancelled | expired | error
	ErrorCode           *string
	ErrorMsg            *string
	SentAt              *time.Time
	ExecutionReceivedAt *time.Time
	RoundTripMs         *int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}
