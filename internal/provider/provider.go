package provider

import (
	"context"
	"time"
)

// ==================== CORE TYPES ====================

type AuthResult struct {
	Balance         float64
	HasOpenPosition bool
	AccountID       string
	Leverage        float64
	BrokerName      string
}

type AccountInfo struct {
	AccountID        string
	Balance          float64
	Leverage         float64
	UsedMargin       float64
	FreeMargin       float64
	AvailableBalance float64
	Currency         string
	BrokerName       string
	IsLive           bool
}

type CredentialStatus struct {
	IsValid     bool
	ExpiresAt   *time.Time
	RefreshedAt time.Time
	Message     string
}

type Position struct {
	PositionID string
	Symbol     string
	Side       string
	Volume     int64
	OpenPrice  float64
	OpenTime   time.Time
	CurrentSL  *float64
	CurrentTP  *float64
	Swap       float64
	Commission float64
	UsedMargin float64
	RawPayload []byte
}

type Candle struct {
	Symbol     string
	Timeframe  string
	OpenTime   int64
	Open       float64
	High       float64
	Low        float64
	Close      float64
	Volume     int64
	ReceivedAt time.Time // when the WebSocket message arrived
}

// ==================== EVENT TYPES ====================

type PriceEvent struct {
	Bid          float64
	Ask          float64
	Mid          float64
	Symbol       string
	ProviderName string
	Timestamp    time.Time
}

type ExecutionEvent struct {
	Type             string
	OrderID          string
	ProviderName     string
	Deal             *DealInfo
	HasDeal          bool
	ClosedPositionID string // set when broker closed position without deal details (TP/SL hit server-side)
	ErrorCode        string
	Timestamp        time.Time
}

type OrderEvent struct {
	OrderID        string
	Side           string
	Volume         int64
	Status         string
	ExecutionPrice *float64
	ProviderName   string
	Timestamp      time.Time
}

type DealInfo struct {
	DealID             int64
	OrderID            int64
	PositionID         int64
	SymbolID           int64
	FilledVolume       int64
	Volume             int64
	ExecutionPrice     float64
	Commission         float64
	TradeSide          uint32
	CreateTime         time.Time
	ExecTime           time.Time
	IsClose            bool
	Close              *CloseInfo
}

type CloseInfo struct {
	ClosedVolume     int64
	EntryPrice       float64
	GrossProfit      float64
	Swap             float64
	Commission       float64
	Balance          float64
	PnLConversionFee float64
}

// ==================== PROVIDER INTERFACE ====================

type Provider interface {
	// === Connection & Auth ===
	Auth(ctx context.Context) (*AuthResult, error)
	Setup() error
	Connect() error
	StartStreaming() error
	Close() error
	Name() string

	// === ORDER PLACEMENT ===
	PlaceMarketOrder(
		ctx context.Context,
		side string,
		volume int64,
		slDist float64,
		tpDist float64,
	) (orderID string, err error)

	PlaceMarketOrderWithTimeout(
		ctx context.Context,
		side string,
		volume int64,
		slDist float64,
		tpDist float64,
		timeout time.Duration,
	) (orderID string, err error)

	CancelOrder(ctx context.Context, orderID string) error

	// === ACCOUNT & POSITIONS ===
	FetchAccountInfo(ctx context.Context) (*AccountInfo, error)
	QueryOpenPositions(ctx context.Context, symbol string) ([]Position, error)

	ClosePosition(
		ctx context.Context,
		positionID string,
		volume int64,
	) (closeOrderID string, err error)

	ReconcilePositions(ctx context.Context) ([]Position, error)

	// === MARKET DATA & SUBSCRIPTIONS ===
	SubscribeCandles(ctx context.Context, symbol, timeframe string) error
	UnsubscribeCandles(ctx context.Context, symbol, timeframe string) error

	FetchHistoricalCandles(
		ctx context.Context,
		symbol string,
		timeframe string,
		count int,
	) ([]Candle, error)

	FetchLatestTick(ctx context.Context, symbol string) (*PriceEvent, error)

	// === CREDENTIALS & REFRESH ===
	RefreshCredentials(ctx context.Context) error
	GetCredentialStatus(ctx context.Context) (*CredentialStatus, error)
	ValidateCredentials(ctx context.Context) error

	SaveBalanceSnapshot(ctx context.Context, balance float64)

	// === EVENT STREAMS ===
	PriceChan() <-chan PriceEvent
	ExecutionChan() <-chan ExecutionEvent
	OrderChan() <-chan OrderEvent
	CandleChan() <-chan Candle
	DisconnectedChan() <-chan struct{}
}
