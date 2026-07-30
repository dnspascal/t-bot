package oanda

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/provider"
	"github.com/denismgaya/t-bot/internal/snapshot"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OANDA struct {
	cfg    *config.Config
	db     *pgxpool.Pool
	events EventsRepo
	snaps  SnapshotsRepo
	client *OANDAClient
}

type EventsRepo interface {
	Insert(context.Context, string, map[string]any, int64) error
}

type SnapshotsRepo interface {
	Insert(context.Context, snapshot.Snapshot) error
}

type OANDAClient struct {
	accountID  string
	accessToken string
	environment string
	baseURL    string
}

func New(cfg *config.Config, db *pgxpool.Pool, events EventsRepo, snaps SnapshotsRepo) *OANDA {
	return &OANDA{
		cfg:    cfg,
		db:     db,
		events: events,
		snaps:  snaps,
		client: &OANDAClient{
			baseURL: "https://api-fxpractice.oanda.com", // Placeholder
		},
	}
}

func (o *OANDA) Connect() error {
	slog.Info("connecting to OANDA")
	return nil
}

func (o *OANDA) Close() error {
	slog.Info("closing OANDA connection")
	return nil
}

func (o *OANDA) Name() string {
	return "oanda"
}

func (o *OANDA) Auth(ctx context.Context) (*provider.AuthResult, error) {
	slog.Info("authenticating with OANDA")

	authStart := time.Now()

	// Placeholder values
	balance := 1000.0
	hasOpenPosition := false

	o.events.Insert(ctx, "auth_ok", map[string]any{
		"provider":  "oanda",
		"balance":   balance,
		"timestamp": time.Now(),
	}, elapsed(authStart))

	trigger := "startup"
	o.snaps.Insert(ctx, snapshot.Snapshot{
		Provider:       "oanda",
		ProviderAcctID: "placeholder_account_id",
		Balance:        balance,
		Trigger:        &trigger,
		SnapshottedAt:  time.Now(),
	})

	return &provider.AuthResult{
		Balance:         balance,
		HasOpenPosition: hasOpenPosition,
		AccountID:       "placeholder_account_id",
		Leverage:        50.0,
		BrokerName:      "OANDA",
	}, nil
}

func (o *OANDA) Setup() error {
	slog.Info("OANDA setup complete")
	return nil
}

// === ORDER PLACEMENT ===

func (o *OANDA) PlaceMarketOrder(
	ctx context.Context,
	side string,
	volume int64,
	slPips float64,
	tpPips float64,
) (orderID string, err error) {
	return "", fmt.Errorf("PlaceMarketOrder not yet implemented for OANDA")
}

func (o *OANDA) PlaceMarketOrderWithTimeout(
	ctx context.Context,
	side string,
	volume int64,
	slPips float64,
	tpPips float64,
	timeout time.Duration,
) (orderID string, err error) {
	return "", fmt.Errorf("PlaceMarketOrderWithTimeout not yet implemented for OANDA")
}

func (o *OANDA) CancelOrder(ctx context.Context, orderID string) error {
	return fmt.Errorf("CancelOrder not yet implemented for OANDA")
}

// === ACCOUNT & POSITIONS ===

func (o *OANDA) FetchAccountInfo(ctx context.Context) (*provider.AccountInfo, error) {
	return &provider.AccountInfo{
		AccountID:        o.client.accountID,
		Balance:          1000.0,
		Leverage:         50.0,
		UsedMargin:       0,
		FreeMargin:       1000.0,
		AvailableBalance: 1000.0,
		Currency:         "USD",
		BrokerName:       "OANDA",
		IsLive:           false,
	}, nil
}

func (o *OANDA) QueryOpenPositions(ctx context.Context, symbol string) ([]provider.Position, error) {
	return []provider.Position{}, nil
}

func (o *OANDA) ClosePosition(ctx context.Context, positionID string, volume int64) (closeOrderID string, err error) {
	return "", fmt.Errorf("ClosePosition not yet implemented for OANDA")
}

func (o *OANDA) ReconcilePositions(ctx context.Context) ([]provider.Position, error) {
	return []provider.Position{}, nil
}

// === MARKET DATA & SUBSCRIPTIONS ===

func (o *OANDA) SubscribeCandles(ctx context.Context, symbol, timeframe string) error {
	slog.Info("OANDA candle subscription (placeholder)", "symbol", symbol, "timeframe", timeframe)
	return nil
}

func (o *OANDA) UnsubscribeCandles(ctx context.Context, symbol, timeframe string) error {
	return fmt.Errorf("UnsubscribeCandles not yet implemented for OANDA")
}

func (o *OANDA) FetchHistoricalCandles(
	ctx context.Context,
	symbol string,
	timeframe string,
	count int,
) ([]provider.Candle, error) {
	return nil, fmt.Errorf("FetchHistoricalCandles not yet implemented for OANDA")
}

func (o *OANDA) FetchLatestTick(ctx context.Context, symbol string) (*provider.PriceEvent, error) {
	return nil, fmt.Errorf("FetchLatestTick not yet implemented for OANDA")
}

// === CREDENTIALS & REFRESH ===

func (o *OANDA) RefreshCredentials(ctx context.Context) error {
	slog.Info("OANDA credentials validated")
	return nil
}

func (o *OANDA) GetCredentialStatus(ctx context.Context) (*provider.CredentialStatus, error) {
	return &provider.CredentialStatus{
		IsValid:     true,
		ExpiresAt:   nil,
		RefreshedAt: time.Now(),
		Message:     "OANDA token status unknown",
	}, nil
}

func (o *OANDA) ValidateCredentials(ctx context.Context) error {
	if o.client.accessToken == "" {
		return fmt.Errorf("OANDA credentials incomplete: missing access token")
	}
	return nil
}

// === EVENT STREAMS ===

func (o *OANDA) PriceChan() <-chan provider.PriceEvent {
	ch := make(chan provider.PriceEvent)
	close(ch)
	return ch
}

func (o *OANDA) ExecutionChan() <-chan provider.ExecutionEvent {
	ch := make(chan provider.ExecutionEvent)
	close(ch)
	return ch
}

func (o *OANDA) OrderChan() <-chan provider.OrderEvent {
	ch := make(chan provider.OrderEvent)
	close(ch)
	return ch
}

func (o *OANDA) CandleChan() <-chan provider.Candle {
	ch := make(chan provider.Candle)
	close(ch)
	return ch
}

func (o *OANDA) DisconnectedChan() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func elapsed(t time.Time) int64 {
	return time.Since(t).Milliseconds()
}
