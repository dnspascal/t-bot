package notify

import "time"

type EventType string

const (
	EventTradeOpened  EventType = "trade_opened"
	EventTradeClosed  EventType = "trade_closed"
	EventDailySummary EventType = "daily_summary"
)

type TradeOpenedPayload struct {
	PositionID string
	Symbol     string
	Side       string
	Price      float64
	SLPrice    float64
	TPPrice    float64
	SLPips     float64
	TPPips     float64
	Strategy   string
	Volume     int64
}

type TradeClosedPayload struct {
	PositionID  string
	Symbol      string
	Side        string
	EntryPrice  float64
	ClosePrice  float64
	Realized    float64
	IsWin       bool
	Duration    time.Duration
	Volume      int64
	CloseReason string
}

type DailySummaryPayload struct {
	Symbol     string
	TradeCount int
	WinCount   int
	LossCount  int
	Realized   float64
	Balance    float64
}
