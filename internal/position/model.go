package position

import "time"

type Position struct {
	ID                 string
	OurOrderID         *string
	Provider           string
	ProviderPositionID string
	ProviderAcctID     string
	SymbolID           string
	Side               string // BUY | SELL
	Volume             int64
	Tier               int
	OpenPrice          *float64
	CurrentSL          *float64
	CurrentTP          *float64
	MaxFavorable       *float64 // best price in trade direction (written on close)
	MaxAdverse         *float64 // worst price against trade direction (written on close)
	Swap               float64
	Commission         float64
	UsedMargin         *float64
	Status             string // created | open | closed | error
	TrailingStopLoss   bool
	GuaranteedStopLoss bool
	Label              *string
	Comment            *string
	DecisionParams     []byte // JSON snapshot of the watcher-level exit/risk constants live at open time
	OpenTimestamp      *time.Time
	CloseTimestamp     *time.Time
	RawPayload         []byte
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Strategy           string // read-only: joined from orders/signals, only populated by OpenByProvider for startup reconcile
}
