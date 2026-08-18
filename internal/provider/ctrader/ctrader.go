package ctrader

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/denismgaya/t-bot/internal/bot"
	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/provider"
	"github.com/denismgaya/t-bot/internal/provider/ctrader/api"
	"github.com/denismgaya/t-bot/internal/snapshot"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CTrader struct {
	cfg      *config.Config
	ctCfg    *config.CTraderConfig
	client   *api.Client
	db       *pgxpool.Pool
	events   EventsRepo
	snaps    SnapshotsRepo
	execCh   chan provider.ExecutionEvent
	priceCh  chan provider.PriceEvent
	candleCh chan provider.Candle
}

type EventsRepo interface {
	Insert(context.Context, string, map[string]any, int64) error
}

type SnapshotsRepo interface {
	Insert(context.Context, snapshot.Snapshot) error
	Latest(context.Context, string, string) (*snapshot.Snapshot, error)
}

func New(cfg *config.Config, client *api.Client, db *pgxpool.Pool, events EventsRepo, snaps SnapshotsRepo) *CTrader {
	c := &CTrader{
		cfg:      cfg,
		ctCfg:    cfg.CTrader,
		client:   client,
		db:       db,
		events:   events,
		snaps:    snaps,
		execCh:   make(chan provider.ExecutionEvent, 200),
		priceCh:  make(chan provider.PriceEvent, 500),
		candleCh: make(chan provider.Candle, 100),
	}
	go c.pipeExecEvents()
	go c.pipePriceEvents()
	go c.pipeCandleEvents()
	return c
}

func (c *CTrader) pipeExecEvents() {
	for event := range c.client.ExecutionCh {
		execEvent := provider.ExecutionEvent{
			Type:          event.Type,
			OrderID:       fmt.Sprintf("%d", event.Deal.OrderID),
			ClientOrderID: event.ClientOrderID,
			BrokerOrderID: event.BrokerOrderID,
			ProviderName:  c.Name(),
			Timestamp:     event.Timestamp,
			HasDeal:       event.HasDeal,
			ErrorCode:     event.ErrorCode,
		}
		if event.ClosedPositionID != 0 {
			execEvent.ClosedPositionID = fmt.Sprintf("%d", event.ClosedPositionID)
		}
		if event.PositionID != 0 {
			execEvent.PositionID = fmt.Sprintf("%d", event.PositionID)
		}
		if event.HasDeal {
			deal := event.Deal
			execEvent.Deal = &provider.DealInfo{
				DealID:         deal.DealID,
				OrderID:        deal.OrderID,
				PositionID:     deal.PositionID,
				SymbolID:       deal.SymbolID,
				FilledVolume:   deal.FilledVolume,
				Volume:         deal.Volume,
				ExecutionPrice: deal.ExecutionPrice,
				Commission:     deal.Commission,
				TradeSide:      deal.TradeSide,
				CreateTime:     deal.CreateTime,
				ExecTime:       deal.ExecTime,
				IsClose:        deal.IsClose,
			}
			if deal.IsClose {
				execEvent.Deal.Close = &provider.CloseInfo{
					ClosedVolume:     deal.Close.ClosedVolume,
					EntryPrice:       deal.Close.EntryPrice,
					GrossProfit:      deal.Close.GrossProfit,
					Swap:             deal.Close.Swap,
					Commission:       deal.Close.Commission,
					Balance:          deal.Close.Balance,
					PnLConversionFee: deal.Close.PnLConversionFee,
				}
			}
		}
		c.execCh <- execEvent
	}
}

func (c *CTrader) Connect() error {
	return c.client.Connect()
}

func (c *CTrader) StartStreaming() error {
	return nil
}

func (c *CTrader) Close() error {
	c.client.Close()
	return nil
}

func (c *CTrader) Name() string {
	return "ctrader"
}

func (c *CTrader) Auth(ctx context.Context) (*provider.AuthResult, error) {
	if token, err := bot.LoadCredential(ctx, c.db, "ctrader_access_token"); err == nil && token != "" {
		c.ctCfg.AccessToken = token
		slog.Info("loaded cTrader access token from DB")
	}
	if token, err := bot.LoadCredential(ctx, c.db, "ctrader_refresh_token"); err == nil && token != "" {
		c.ctCfg.RefreshToken = token
		slog.Info("loaded cTrader refresh token from DB")
	}

	authStart := time.Now()
	if err := c.client.AuthApp(c.ctCfg.ClientID, c.ctCfg.ClientSecret); err != nil {
		c.events.Insert(ctx, "auth_fail", map[string]any{"error": err.Error(), "stage": "app_auth"}, elapsed(authStart))
		return nil, fmt.Errorf("app auth: %w", err)
	}
	time.Sleep(2 * time.Second)

	accounts, err := c.client.GetAccountList(c.ctCfg.AccessToken)
	if err != nil {
		slog.Warn("GetAccountList failed — refreshing credentials and retrying", "err", err)
		if rfErr := c.RefreshCredentials(ctx); rfErr != nil {
			return nil, fmt.Errorf("get account list: %w (refresh also failed: %v)", err, rfErr)
		}
		// Reconnect TCP — connection may have dropped during the OAuth wait.
		if err := c.client.Connect(); err != nil {
			return nil, fmt.Errorf("reconnect after token refresh: %w", err)
		}
		if err := c.client.AuthApp(c.ctCfg.ClientID, c.ctCfg.ClientSecret); err != nil {
			return nil, fmt.Errorf("re-auth app after token refresh: %w", err)
		}
		time.Sleep(2 * time.Second)
		accounts, err = c.client.GetAccountList(c.ctCfg.AccessToken)
		if err != nil {
			return nil, fmt.Errorf("get account list after refresh: %w", err)
		}
	}

	var ctidAccountID int64
	var accountBrokerName string
	mode := "demo"
	if c.ctCfg.Demo {
		mode = "demo"
	} else {
		mode = "live"
	}
	for _, acc := range accounts {
		if acc.IsLive == !c.ctCfg.Demo {
			ctidAccountID = acc.CtidTraderAccountID
			accountBrokerName = acc.BrokerName
			slog.Info("found trading account",
				"ctidTraderAccountID", acc.CtidTraderAccountID,
				"traderLogin", acc.TraderLogin,
				"isLive", acc.IsLive,
				"broker", acc.BrokerName,
			)
			break
		}
	}
	if ctidAccountID == 0 {
		return nil, fmt.Errorf("no %s account found in account list (got %d accounts)", mode, len(accounts))
	}
	c.client.SetAccountID(ctidAccountID)

	if err := c.client.AuthAccount(c.ctCfg.AccessToken); err != nil {
		c.events.Insert(ctx, "auth_fail", map[string]any{"error": err.Error(), "stage": "account_auth"}, elapsed(authStart))
		return nil, fmt.Errorf("account auth: %w", err)
	}
	c.events.Insert(ctx, "auth_ok", map[string]any{"account_id": c.ctCfg.AccountID}, elapsed(authStart))
	time.Sleep(2 * time.Second)

	fetchStart := time.Now()
	traderInfo, err := c.client.FetchAccountInfo()
	if err != nil {
		return nil, fmt.Errorf("fetch account info: %w", err)
	}
	if traderInfo.BrokerName == "" {
		traderInfo.BrokerName = accountBrokerName
	}
	balance := traderInfo.Balance
	if balance <= 0 {
		provAcctID := fmt.Sprintf("%d", ctidAccountID)
		if latest, err := c.snaps.Latest(ctx, "ctrader", provAcctID); err == nil && latest.Balance > 0 {
			balance = latest.Balance
			slog.Info("ProtoOATraderRes returned no balance — using latest DB snapshot", "balance", balance)
		} else if c.ctCfg.InitialBalance > 0 {
			balance = c.ctCfg.InitialBalance
			slog.Info("ProtoOATraderRes returned no balance — using CTRADER_INITIAL_BALANCE", "balance", balance)
		}
	}
	leverage := traderInfo.Leverage
	maxLeverage := traderInfo.MaxLeverage
	accountMode := traderInfo.AccountMode
	brokerName := traderInfo.BrokerName
	isLimitedRisk := traderInfo.IsLimitedRisk
	fairStopOut := traderInfo.FairStopOut
	trigger := "startup"
	c.snaps.Insert(ctx, snapshot.Snapshot{
		Provider:       "ctrader",
		ProviderAcctID: fmt.Sprintf("%d", c.ctCfg.AccountID),
		Balance:        balance,
		LeverageRatio:  &leverage,
		MaxLeverage:    &maxLeverage,
		AccountMode:    &accountMode,
		BrokerName:     &brokerName,
		IsLimitedRisk:  &isLimitedRisk,
		FairStopOut:    &fairStopOut,
		Trigger:        &trigger,
		SnapshottedAt:  time.Now(),
	})

	c.events.Insert(ctx, "account_snapshot", map[string]any{
		"balance":  balance,
		"leverage": leverage,
		"broker":   brokerName,
	}, elapsed(fetchStart))

	reconcileStart := time.Now()
	openPositions, err := c.client.Reconcile()
	if err != nil {
		return nil, fmt.Errorf("reconcile: %w", err)
	}

	hasOpenPosition := false
	for _, pos := range openPositions {
		if pos.SymbolID == c.ctCfg.SymbolID {
			hasOpenPosition = true
			break
		}
	}
	slog.Info("startup reconcile", "openPositions", len(openPositions), "hasOpenPosition", hasOpenPosition)
	c.events.Insert(ctx, "reconcile", map[string]any{
		"open_positions":    len(openPositions),
		"has_open_position": hasOpenPosition,
	}, elapsed(reconcileStart))

	return &provider.AuthResult{
		Balance:         balance,
		HasOpenPosition: hasOpenPosition,
		AccountID:       fmt.Sprintf("%d", ctidAccountID),
		Leverage:        leverage,
		BrokerName:      brokerName,
	}, nil
}

func elapsed(t time.Time) int64 {
	return time.Since(t).Milliseconds()
}

func (c *CTrader) PlaceMarketOrder(
	ctx context.Context,
	side string,
	volume int64,
	slDist float64,
	tpDist float64,
	label string,
) (orderID string, err error) {
	sideUint32 := stringToSide(side)
	err = c.client.PlaceMarketOrder(sideUint32, volume, slDist, tpDist, label)

	return "", err
}

func (c *CTrader) PlaceMarketOrderWithTimeout(
	ctx context.Context,
	side string,
	volume int64,
	slDist float64,
	tpDist float64,
	timeout time.Duration,
	label string,
) (orderID string, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := c.PlaceMarketOrder(ctx, side, volume, slDist, tpDist, label)
		done <- err
	}()

	select {
	case err := <-done:
		return "", err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (c *CTrader) CancelOrder(ctx context.Context, orderID string) error {
	return fmt.Errorf("CancelOrder not yet implemented for cTrader")
}

func (c *CTrader) FetchAccountInfo(ctx context.Context) (*provider.AccountInfo, error) {
	info, err := c.client.FetchAccountInfo()
	if err != nil {
		return nil, err
	}
	return &provider.AccountInfo{
		AccountID:        fmt.Sprintf("%d", c.ctCfg.AccountID),
		Balance:          info.Balance,
		Leverage:         info.Leverage,
		UsedMargin:       0,
		FreeMargin:       0,
		AvailableBalance: info.Balance,
		Currency:         "USD",
		BrokerName:       info.BrokerName,
		IsLive:           !c.ctCfg.Demo,
	}, nil
}

func (c *CTrader) QueryOpenPositions(ctx context.Context, symbol string) ([]provider.Position, error) {
	positions, err := c.client.Reconcile()
	if err != nil {
		return nil, err
	}

	var result []provider.Position
	for _, pos := range positions {
		if pos.SymbolID != c.ctCfg.SymbolID {
			continue
		}
		posID := fmt.Sprintf("%d", pos.PositionID)
		side := "BUY"
		if pos.Side == api.TradeSideSell {
			side = "SELL"
		}
		result = append(result, provider.Position{
			PositionID: posID,
			Symbol:     symbol,
			Side:       side,
			Volume:     pos.Volume,
			OpenPrice:  0,
			OpenTime:   time.Now(),
			CurrentSL:  nil,
			CurrentTP:  nil,
			Swap:       0,
			Commission: 0,
			UsedMargin: 0,
		})
	}
	return result, nil
}

func (c *CTrader) ClosePosition(ctx context.Context, positionID string, volume int64) (string, error) {
	var posID int64
	if _, err := fmt.Sscanf(positionID, "%d", &posID); err != nil {
		return "", fmt.Errorf("invalid positionID %q: %w", positionID, err)
	}
	if err := c.client.ClosePosition(posID, volume); err != nil {
		return "", fmt.Errorf("ClosePosition: %w", err)
	}
	return "", nil
}

func (c *CTrader) AmendPositionSL(ctx context.Context, positionID string, newSLPrice, tpPrice float64) error {
	var posID int64
	if _, err := fmt.Sscanf(positionID, "%d", &posID); err != nil {
		return fmt.Errorf("invalid positionID %q: %w", positionID, err)
	}
	return c.client.AmendPositionSL(posID, newSLPrice, tpPrice)
}

// ClosePositionVariant sends a close with alternative proto field numbers.
// Used by the /testclose diagnostic to detect field-number mismatches.
func (c *CTrader) ClosePositionVariant(positionID string, volume int64, posIDField, volField int) error {
	var posID int64
	if _, err := fmt.Sscanf(positionID, "%d", &posID); err != nil {
		return fmt.Errorf("invalid positionID %q: %w", positionID, err)
	}
	return c.client.ClosePositionVariant(posID, volume, posIDField, volField)
}

func (c *CTrader) ReconcilePositions(ctx context.Context) ([]provider.Position, error) {
	return c.QueryOpenPositions(ctx, "")
}

func (c *CTrader) FetchClosedDeal(positionID string, openTime time.Time) (*provider.DealInfo, error) {
	var posID int64
	if _, err := fmt.Sscanf(positionID, "%d", &posID); err != nil {
		return nil, fmt.Errorf("FetchClosedDeal: invalid positionID %q: %w", positionID, err)
	}
	from := openTime
	if from.IsZero() {
		from = time.Now().Add(-48 * time.Hour)
	}
	d, err := c.client.GetDealsForPosition(posID, from)
	if err != nil || d == nil {
		return nil, err
	}
	info := &provider.DealInfo{
		DealID:         d.DealID,
		OrderID:        d.OrderID,
		PositionID:     d.PositionID,
		FilledVolume:   d.FilledVolume,
		Volume:         d.Volume,
		ExecutionPrice: d.ExecutionPrice,
		Commission:     d.Commission,
		TradeSide:      d.TradeSide,
		CreateTime:     d.CreateTime,
		ExecTime:       d.ExecTime,
		IsClose:        true,
	}
	if d.IsClose {
		info.Close = &provider.CloseInfo{
			ClosedVolume:     d.Close.ClosedVolume,
			EntryPrice:       d.Close.EntryPrice,
			GrossProfit:      d.Close.GrossProfit,
			Swap:             d.Close.Swap,
			Commission:       d.Close.Commission,
			Balance:          d.Close.Balance,
			PnLConversionFee: d.Close.PnLConversionFee,
		}
	}
	return info, nil
}

func (c *CTrader) SubscribeCandles(ctx context.Context, symbol, timeframe string) error {
	period := stringToPeriod(timeframe)
	return c.client.SubscribeLiveTrendbar(period)
}

func (c *CTrader) UnsubscribeCandles(ctx context.Context, symbol, timeframe string) error {
	return fmt.Errorf("UnsubscribeCandles not yet implemented for cTrader")
}

func (c *CTrader) FetchHistoricalCandles(
	ctx context.Context,
	symbol string,
	timeframe string,
	count int,
) ([]provider.Candle, error) {
	period := stringToPeriod(timeframe)
	bars, err := c.client.FetchHistoricalTrendbars(period, count)
	if err != nil {
		return nil, err
	}

	var result []provider.Candle
	for _, bar := range bars {
		result = append(result, provider.Candle{
			Symbol:    symbol,
			Timeframe: timeframe,
			OpenTime:  bar.OpenTime,
			Open:      bar.Open,
			High:      bar.High,
			Low:       bar.Low,
			Close:     bar.Close,
			Volume:    bar.Volume,
		})
	}
	return result, nil
}

func (c *CTrader) FetchLatestTick(ctx context.Context, symbol string) (*provider.PriceEvent, error) {
	return nil, fmt.Errorf("FetchLatestTick not yet implemented for cTrader")
}

func (c *CTrader) RefreshCredentials(ctx context.Context) error {
	newAccessToken, newRefreshToken, err := api.RefreshToken(c.ctCfg.ClientID, c.ctCfg.ClientSecret, c.ctCfg.RefreshToken)
	if err != nil {
		// The other bot instance may have already refreshed and saved a newer token to the DB.
		// Try that before falling back to the OAuth browser flow.
		if dbToken, dbErr := bot.LoadCredential(ctx, c.db, "ctrader_refresh_token"); dbErr == nil && dbToken != "" && dbToken != c.ctCfg.RefreshToken {
			slog.Info("token refresh failed — retrying with newer token from DB", "err", err)
			c.ctCfg.RefreshToken = dbToken
			newAccessToken, newRefreshToken, err = api.RefreshToken(c.ctCfg.ClientID, c.ctCfg.ClientSecret, dbToken)
		}
	}
	if err != nil {
		slog.Warn("token refresh failed — initiating OAuth flow", "err", err)
		newAccessToken, newRefreshToken, err = api.InitiateOAuthFlow(
			c.ctCfg.ClientID, c.ctCfg.ClientSecret,
			c.ctCfg.OAuthRedirectURI, c.ctCfg.OAuthCallbackPort,
		)
		if err != nil {
			return fmt.Errorf("oauth flow failed: %w", err)
		}
	}
	c.ctCfg.AccessToken = newAccessToken
	c.ctCfg.RefreshToken = newRefreshToken
	if err := bot.SaveCredential(ctx, c.db, "ctrader_access_token", newAccessToken); err != nil {
		slog.Warn("failed to persist cTrader access token", "err", err)
	}
	if err := bot.SaveCredential(ctx, c.db, "ctrader_refresh_token", newRefreshToken); err != nil {
		slog.Warn("failed to persist cTrader refresh token", "err", err)
	}
	slog.Info("cTrader credentials refreshed")
	return nil
}

func (c *CTrader) GetCredentialStatus(ctx context.Context) (*provider.CredentialStatus, error) {
	valid := c.ctCfg.AccessToken != ""
	return &provider.CredentialStatus{
		IsValid:     valid,
		ExpiresAt:   nil,
		RefreshedAt: time.Now(),
		Message:     "cTrader token status unknown (no expiration info)",
	}, nil
}

func (c *CTrader) ValidateCredentials(ctx context.Context) error {
	if c.ctCfg.ClientID == "" || c.ctCfg.ClientSecret == "" {
		return fmt.Errorf("cTrader credentials incomplete: missing ClientID or ClientSecret")
	}
	if c.ctCfg.AccessToken == "" {
		return fmt.Errorf("cTrader AccessToken not set")
	}
	return nil
}

func (c *CTrader) pipePriceEvents() {
	for event := range c.client.PriceCh {
		c.priceCh <- provider.PriceEvent{
			Bid:          event.Bid,
			Ask:          event.Ask,
			Mid:          event.Mid,
			Symbol:       "",
			ProviderName: c.Name(),
			Timestamp:    event.Timestamp,
		}
	}
}

func (c *CTrader) pipeCandleEvents() {
	type pendingBar struct {
		openTime int64
		candle   provider.Candle
	}
	pending := make(map[string]pendingBar)

	for bar := range c.client.TrendbarCh {
		period := api.PeriodToString(bar.Period)
		if period == "UNKNOWN" {
			slog.Warn("ctrader: trendbar with unknown period", "periodCode", bar.Period)
			continue
		}
		current := provider.Candle{
			Timeframe:  period,
			OpenTime:   bar.OpenTime,
			Open:       bar.Open,
			High:       bar.High,
			Low:        bar.Low,
			Close:      bar.Close,
			Volume:     bar.Volume,
			ReceivedAt: time.Now(),
		}
		if prev, ok := pending[period]; ok && prev.openTime != bar.OpenTime {
			c.candleCh <- prev.candle
		}
		pending[period] = pendingBar{openTime: bar.OpenTime, candle: current}
	}
}

func (c *CTrader) PriceChan() <-chan provider.PriceEvent {
	return c.priceCh
}

func (c *CTrader) ExecutionChan() <-chan provider.ExecutionEvent {
	return c.execCh
}

func (c *CTrader) OrderChan() <-chan provider.OrderEvent {
	out := make(chan provider.OrderEvent, 100)
	go func() {
		for event := range c.client.ExecutionCh {
			orderEvent := provider.OrderEvent{
				OrderID:      fmt.Sprintf("%d", event.Deal.OrderID),
				Side:         sideToString(event.Deal.TradeSide),
				ProviderName: c.Name(),
				Timestamp:    event.Timestamp,
				Volume:       event.Deal.Volume,
			}
			price := event.Deal.ExecutionPrice
			orderEvent.ExecutionPrice = &price
			out <- orderEvent
		}
		close(out)
	}()
	return out
}

func (c *CTrader) CandleChan() <-chan provider.Candle {
	return c.candleCh
}

func (c *CTrader) DisconnectedChan() <-chan struct{} {
	return c.client.Dead()
}

func (c *CTrader) SaveBalanceSnapshot(ctx context.Context, balance float64) {
	provAcctID := fmt.Sprintf("%d", c.client.AccountID())
	trigger := "post_trade"
	err := c.snaps.Insert(ctx, snapshot.Snapshot{
		Provider:       "ctrader",
		ProviderAcctID: provAcctID,
		Balance:        balance,
		Trigger:        &trigger,
		SnapshottedAt:  time.Now(),
	})
	if err != nil {
		slog.Error("SaveBalanceSnapshot failed", "err", err)
	}
}

func stringToSide(side string) uint32 {
	if side == "BUY" {
		return api.TradeSideBuy
	}
	return api.TradeSideSell
}

func sideToString(side uint32) string {
	if side == api.TradeSideBuy {
		return "BUY"
	}
	return "SELL"
}

func stringToPeriod(tf string) uint32 {
	switch tf {
	case "M1":
		return api.PeriodM1
	case "M2":
		return api.PeriodM2
	case "M3":
		return api.PeriodM3
	case "M4":
		return api.PeriodM4
	case "M5":
		return api.PeriodM5
	case "M10":
		return api.PeriodM10
	case "M15":
		return api.PeriodM15
	case "M30":
		return api.PeriodM30
	case "H1":
		return api.PeriodH1
	case "H4":
		return api.PeriodH4
	case "D1":
		return api.PeriodD1
	case "W1":
		return api.PeriodW1
	case "MN1":
		return api.PeriodMN1
	default:
		return api.PeriodM5
	}
}
