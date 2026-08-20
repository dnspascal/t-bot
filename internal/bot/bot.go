package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/denismgaya/t-bot/internal/candle"
	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/event"
	"github.com/denismgaya/t-bot/internal/fill"
	"github.com/denismgaya/t-bot/internal/indicator"
	"github.com/denismgaya/t-bot/internal/marketstate"
	"github.com/denismgaya/t-bot/internal/notify"
	"github.com/denismgaya/t-bot/internal/order"
	"github.com/denismgaya/t-bot/internal/pnl"
	"github.com/denismgaya/t-bot/internal/position"
	"github.com/denismgaya/t-bot/internal/provider"
	"github.com/denismgaya/t-bot/internal/risk"
	"github.com/denismgaya/t-bot/internal/signal"
	strat "github.com/denismgaya/t-bot/internal/strategy"
	"github.com/denismgaya/t-bot/internal/symbol"
	"github.com/denismgaya/t-bot/internal/tick"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pendingOrderTimeout = 30 * time.Second
const pendingCloseTimeout = 30 * time.Second

type pendingClose struct {
	reason string
	sentAt time.Time
}

type pendingOrderState struct {
	ourOrderID    string
	sentAt        time.Time
	side          string
	tier          int
	slPrice       float64
	tpPrice       float64
	atr           float64
	strategyName  string
	brokerOrderID int64
}

type Bot struct {
	cfg          *config.Config
	provider     provider.Provider
	riskMgr      *risk.Manager
	currentPrice provider.PriceEvent
	registry     *PositionRegistry
	strategies   []strat.Strategy

	symbol         string
	symbolUUID     string
	providerAcctID string
	pipSize        float64

	lotUnit int64

	balanceMu sync.Mutex
	balance   float64
	leverage  float64

	pendingOrders      map[string]*pendingOrderState
	brokerOrderIDs     map[int64]string // broker order ID → our clientOrderID
	lastCandleOpenTime int64
	lastCandleClose    float64

	forceTestOrder bool

	pendingCloseReasons map[string]pendingClose

	pendingBreakEvenMu sync.Mutex
	pendingBreakEven   map[string]chan struct{} // providerPositionID -> signaled when broker confirms the SL amend

	dispatcher notify.Dispatcher

	pausedMu sync.Mutex
	paused   bool

	refresherOnce     sync.Once
	watchDogOnce      sync.Once
	tickWriterOnce    sync.Once
	weekendCloserOnce sync.Once
	dailySummaryOnce  sync.Once

	tickCh            chan tick.Tick
	lastTickSaved     time.Time
	lastDrawbackCheck time.Time

	testCloseMode    atomic.Bool
	testCloseTrigger chan struct{}

	testAmendMode    atomic.Bool
	testAmendTrigger chan struct{}

	db        *pgxpool.Pool
	lookup    *symbol.SymbolLookup
	ticks     *tick.Repository
	candles   *candle.Repository
	signals   *signal.Repository
	orders    *order.Repository
	fills     *fill.Repository
	positions *position.Repository
	pnls      *pnl.Repository
	events    *event.Repository

	processorMgr *marketstate.ProcessorManager
	marketStates map[string]map[string]indicator.MarketState
}

func New(
	cfg *config.Config,
	prov provider.Provider,
	strategies []strat.Strategy,
	sym string,
	symbolUUID string,
	providerAcctID string,
	pipSize float64,
	lotUnit int64,
	db *pgxpool.Pool,
	riskMgr *risk.Manager,
	balance float64,
	leverage float64,
	lookup *symbol.SymbolLookup,
	ticks *tick.Repository,
	candles *candle.Repository,
	signals *signal.Repository,
	orders *order.Repository,
	fills *fill.Repository,
	positions *position.Repository,
	pnls *pnl.Repository,
	events *event.Repository,
	processorMgr *marketstate.ProcessorManager,
	dispatcher notify.Dispatcher,
) *Bot {
	return &Bot{
		cfg:                 cfg,
		provider:            prov,
		strategies:          strategies,
		symbol:              sym,
		symbolUUID:          symbolUUID,
		providerAcctID:      providerAcctID,
		pipSize:             pipSize,
		lotUnit:             lotUnit,
		db:                  db,
		riskMgr:             riskMgr,
		balance:             balance,
		leverage:            leverage,
		registry:            newPositionRegistry(),
		pendingOrders:       make(map[string]*pendingOrderState),
		brokerOrderIDs:      make(map[int64]string),
		pendingCloseReasons: make(map[string]pendingClose),
		pendingBreakEven:    make(map[string]chan struct{}),
		tickCh:              make(chan tick.Tick, 500),
		testCloseTrigger:    make(chan struct{}, 1),
		testAmendTrigger:    make(chan struct{}, 1),
		lookup:              lookup,
		ticks:               ticks,
		candles:             candles,
		signals:             signals,
		orders:              orders,
		fills:               fills,
		positions:           positions,
		pnls:                pnls,
		events:              events,
		processorMgr:        processorMgr,
		marketStates:        make(map[string]map[string]indicator.MarketState),
		dispatcher:          dispatcher,
	}
}

func (b *Bot) Run(ctx context.Context, startedAt time.Time) {
	b.reconcileOpenPositions(ctx)

	b.refresherOnce.Do(func() { go b.tokenRefresher(ctx) })
	b.watchDogOnce.Do(func() { go b.botWatchDog(ctx) })
	b.tickWriterOnce.Do(func() { go b.tickWriter(ctx) })
	if b.provider.Name() == "ctrader" {
		b.weekendCloserOnce.Do(func() { go b.weekendPositionCloser(ctx) })
	}
	b.dailySummaryOnce.Do(func() { go b.dailySummarySender(ctx) })

	priceCh := b.provider.PriceChan()
	candleCh := b.provider.CandleChan()
	execCh := b.provider.ExecutionChan()
	discCh := b.provider.DisconnectedChan()

	if b.cfg.SendTestPosition {
		b.forceTestOrder = true
		slog.Warn("SEND_TEST_POSITION=true — will place one real BUY on next M5 close via full pipeline")
	}

	for {
		select {
		case <-ctx.Done():
			b.events.Insert(context.Background(), "stopped", map[string]any{
				"uptime_ms": ms(startedAt),
			}, ms(startedAt))
			slog.Info("shutdown complete", "uptimeMs", ms(startedAt))
			return

		case price := <-priceCh:
			b.onTick(ctx, price)

		case c := <-candleCh:
			b.onCandleReceived(ctx, c)

		case exec := <-execCh:
			b.onExecution(ctx, exec)

		case <-b.testCloseTrigger:
			slog.Info("TESTCLOSE: opening test position to diagnose close rejection")
			b.testCloseMode.Store(true)
			b.sendTestPosition(ctx)

		case <-b.testAmendTrigger:
			slog.Info("TESTAMEND: opening test position to diagnose SL amend confirmation")
			b.testAmendMode.Store(true)
			b.sendTestPosition(ctx)

		case <-discCh:
			slog.Error("provider connection lost — bot stopping")
			return
		}
	}
}

func (b *Bot) reconcileOpenPositions(ctx context.Context) {
	dbPositions, err := b.positions.OpenByProvider(ctx, b.provider.Name(), b.symbolUUID)
	if err != nil {
		slog.Error("startup reconcile: failed to query open positions", "err", err)
		return
	}
	if len(dbPositions) == 0 {
		return
	}

	brokerOpen := make(map[string]bool)
	reconcileOK := false
	brokerPositions, err := b.provider.ReconcilePositions(ctx)
	if err != nil {
		slog.Warn("startup reconcile: could not fetch broker positions, trusting DB",
			"provider", b.provider.Name(), "err", err,
		)
	} else {
		reconcileOK = true
		for _, bp := range brokerPositions {
			brokerOpen[bp.PositionID] = true
		}
	}

	loaded, purged := 0, 0
	for _, p := range dbPositions {
		if p.ProviderPositionID == "" {
			slog.Warn("startup reconcile: skipping position with empty provider ID",
				"provider", b.provider.Name(), "dbID", p.ID,
			)
			continue
		}

		if reconcileOK && !brokerOpen[p.ProviderPositionID] {
			slog.Warn("startup reconcile: position closed at broker while bot was offline — purging",
				"provider", b.provider.Name(), "posID", p.ProviderPositionID,
			)
			b.reconcileOfflineClose(ctx, p)
			purged++
			continue
		}

		var openPrice, sl, tp float64
		if p.OpenPrice != nil {
			openPrice = *p.OpenPrice
		}
		if p.CurrentSL != nil {
			sl = *p.CurrentSL
		}
		if p.CurrentTP != nil {
			tp = *p.CurrentTP
		}
		var openTime time.Time
		if p.OpenTimestamp != nil {
			openTime = *p.OpenTimestamp
		}
		maxFavorable, maxAdverse := b.recoverPeaks(ctx, p.SymbolID, p.Side, openPrice, openTime)
		b.registry.Register(trackedPosition{
			ProviderPositionID: p.ProviderPositionID,
			Side:               p.Side,
			Volume:             p.Volume,
			OpenPrice:          openPrice,
			SLPrice:            sl,
			TPPrice:            tp,
			OpenTime:           openTime,
			Tier:               p.Tier,
			MaxFavorable:       maxFavorable,
			MaxAdverse:         maxAdverse,
		})
		slog.Info("startup reconcile: loaded open position",
			"provider", b.provider.Name(),
			"maxFavorable", maxFavorable,
			"maxAdverse", maxAdverse,
			"posID", p.ProviderPositionID,
			"side", p.Side,
			"openPrice", openPrice,
			"volume", p.Volume,
		)
		loaded++
	}
	slog.Info("startup reconcile complete",
		"provider", b.provider.Name(),
		"loaded", loaded,
		"purged", purged,
	)
}

func (b *Bot) recoverPeaks(ctx context.Context, symbolID, side string, openPrice float64, openTime time.Time) (maxFavorable, maxAdverse float64) {
	if openTime.IsZero() {
		return 0, 0
	}
	candles, err := b.candles.Since(ctx, symbolID, config.PeriodM5, openTime)
	if err != nil {
		slog.Warn("recoverPeaks: candle lookup failed — falling back to open price", "symbolID", symbolID, "err", err)
		return 0, 0
	}
	if len(candles) == 0 {
		return 0, 0
	}
	maxFavorable, maxAdverse = openPrice, openPrice
	for _, c := range candles {
		if side == config.SignalBuy {
			if c.Close > maxFavorable {
				maxFavorable = c.Close
			}
			if c.Close < maxAdverse {
				maxAdverse = c.Close
			}
		} else {
			if c.Close < maxFavorable {
				maxFavorable = c.Close
			}
			if c.Close > maxAdverse {
				maxAdverse = c.Close
			}
		}
	}
	return maxFavorable, maxAdverse
}

type dealFetcher interface {
	FetchClosedDeal(positionID string, openTime time.Time) (*provider.DealInfo, error)
}

func (b *Bot) reconcileOfflineClose(ctx context.Context, p position.Position) {
	posID := p.ProviderPositionID
	var openTime time.Time
	if p.OpenTimestamp != nil {
		openTime = *p.OpenTimestamp
	}

	df, canFetch := b.provider.(dealFetcher)
	if !canFetch {
		slog.Warn("startup reconcile: provider does not support deal history — position marked closed without PnL",
			"posID", posID,
		)
		if err := b.positions.Close(ctx, b.provider.Name(), posID, time.Now(), nil, nil); err != nil {
			slog.Error("startup reconcile: positions.Close failed", "posID", posID, "err", err)
		}
		return
	}

	deal, err := df.FetchClosedDeal(posID, openTime)
	if err != nil {
		slog.Error("startup reconcile: FetchClosedDeal failed — marking closed without PnL",
			"posID", posID, "err", err,
		)
		if dbErr := b.positions.Close(ctx, b.provider.Name(), posID, time.Now(), nil, nil); dbErr != nil {
			slog.Error("startup reconcile: positions.Close failed", "posID", posID, "err", dbErr)
		}
		return
	}
	if deal == nil {
		slog.Warn("startup reconcile: close deal not found in broker history — marking closed without PnL",
			"posID", posID,
		)
		if err := b.positions.Close(ctx, b.provider.Name(), posID, time.Now(), nil, nil); err != nil {
			slog.Error("startup reconcile: positions.Close failed", "posID", posID, "err", err)
		}
		return
	}

	cl := deal.Close
	if cl == nil {
		slog.Warn("startup reconcile: deal has no closePositionDetail — marking closed without PnL",
			"posID", posID, "dealID", deal.DealID,
		)
		if err := b.positions.Close(ctx, b.provider.Name(), posID, deal.ExecTime, nil, nil); err != nil {
			slog.Error("startup reconcile: positions.Close failed", "posID", posID, "err", err)
		}
		return
	}

	closeTime := deal.ExecTime
	if closeTime.IsZero() {
		closeTime = time.Now()
	}
	if err := b.positions.Close(ctx, b.provider.Name(), posID, closeTime, nil, nil); err != nil {
		slog.Error("startup reconcile: positions.Close failed", "posID", posID, "err", err)
	}

	closeSide := config.SignalSell
	if deal.TradeSide == 1 {
		closeSide = config.SignalBuy
	}
	provPosID := posID
	reason := strat.CloseReasonSLHit
	if cl.GrossProfit >= 0 {
		reason = strat.CloseReasonTPHit
	}
	dealID := fmt.Sprintf("%d", deal.DealID)
	orderID := fmt.Sprintf("%d", deal.OrderID)
	entryPrice := cl.EntryPrice
	closedVolume := cl.ClosedVolume
	grossProfit := cl.GrossProfit
	swap := cl.Swap
	closeCommission := cl.Commission
	balanceAfter := cl.Balance
	pnlFee := cl.PnLConversionFee
	dealCommission := deal.Commission

	if err := b.fills.Insert(ctx, fill.Fill{
		Provider:           b.provider.Name(),
		ProviderFillID:     dealID,
		ProviderOrderID:    &orderID,
		ProviderPositionID: &provPosID,
		SymbolID:           b.symbolUUID,
		Side:               closeSide,
		Volume:             &deal.Volume,
		FilledVolume:       &deal.FilledVolume,
		ExecutionPrice:     &deal.ExecutionPrice,
		EventType:          "close",
		Commission:         &dealCommission,
		CloseEntryPrice:    &entryPrice,
		GrossProfit:        &grossProfit,
		CloseSwap:          &swap,
		CloseCommission:    &closeCommission,
		BalanceAfter:       &balanceAfter,
		ClosedVolume:       &closedVolume,
		PnLConversionFee:   &pnlFee,
		CloseReason:        &reason,
		ProviderCreateTime: &deal.CreateTime,
		ProviderExecTime:   &deal.ExecTime,
		ReceivedAt:         time.Now(),
	}); err != nil {
		slog.Error("startup reconcile: fills.Insert failed", "posID", posID, "err", err)
	}

	realized := cl.GrossProfit + cl.Commission + cl.Swap
	isWin := realized > 0
	if err := b.pnls.Upsert(ctx, b.symbolUUID, realized, cl.GrossProfit, cl.Commission, cl.Swap, isWin, 0, 0); err != nil {
		slog.Error("startup reconcile: pnls.Upsert failed", "posID", posID, "err", err)
	}
	b.riskMgr.RecordTrade(realized)

	slog.Info("startup reconcile: offline close recorded from broker history",
		"posID", posID,
		"dealID", deal.DealID,
		"closePrice", deal.ExecutionPrice,
		"grossProfit", cl.GrossProfit,
		"realized", realized,
		"reason", reason,
	)
}

func (b *Bot) onCandleReceived(ctx context.Context, c provider.Candle) {
	dbCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	b.storeCandle(dbCtx, c)

	states, err := b.processorMgr.ProcessCandle(dbCtx, c.Timeframe, c.OpenTime, c.Open, c.High, c.Low, c.Close, c.Volume, c.ReceivedAt)
	if err != nil {
		slog.Error("process candle failed", "timeframe", c.Timeframe, "err", err)
	}
	if b.marketStates[b.symbolUUID] == nil {
		b.marketStates[b.symbolUUID] = make(map[string]indicator.MarketState)
	}
	maps.Copy(b.marketStates[b.symbolUUID], states)

	switch c.Timeframe {
	case "M1":
		if b.registry.Count() > 0 {
			b.checkPeakDrawback(ctx, c.Close)
			b.logM1State(c.Close)
		}
	case "M5":
		if c.OpenTime != b.lastCandleOpenTime {
			if b.lastCandleOpenTime != 0 {
				b.processClosedCandle(ctx, b.lastCandleClose)
			}
			b.lastCandleOpenTime = c.OpenTime
		}
		b.lastCandleClose = c.Close
	}
}

func (b *Bot) processClosedCandle(ctx context.Context, _ float64) {
	for id, ps := range b.pendingOrders {
		if time.Since(ps.sentAt) > pendingOrderTimeout {
			slog.Warn("pending order timed out — clearing",
				"orderID", ps.ourOrderID,
				"elapsed", time.Since(ps.sentAt).Round(time.Second),
			)
			b.orders.UpdateError(ctx, ps.ourOrderID, "TIMEOUT", "no execution event received")
			delete(b.pendingOrders, id)
			if ps.brokerOrderID != 0 {
				delete(b.brokerOrderIDs, ps.brokerOrderID)
			}
		}
	}

	if !b.processorMgr.AllWarmedUp() {
		slog.Info("warming up indicators")
		return
	}

	states := b.marketStates[b.symbolUUID]
	m5, ok := states["M5"]
	if !ok {
		return
	}

	mid := b.currentPrice.Mid
	if mid == 0 {
		mid = (b.currentPrice.Bid + b.currentPrice.Ask) / 2
	}

	b.logUnrealizedPnL(mid)

	b.watchPositions(ctx, m5)

	barTime := time.Unix(m5.BarTime, 0).UTC()
	snapshots := buildMarketStateSnapshots(states)

	for _, s := range b.strategies {
		evalStart := time.Now()
		var result strat.EntryResult
		if isEODWindow() {
			result = strat.EntryResult{Signal: config.SignalHold, Reason: "EOD window — no new entries before dead session"}
		} else {
			result = s.Evaluate(states, mid, b.pipSize)
		}
		if result.StrategyName == "" {
			result.StrategyName = s.Name()
		}

		if b.forceTestOrder && result.Signal == config.SignalHold {
			slog.Warn("FORCE_TEST_ORDER: overriding HOLD with BUY for pipeline test")
			result = strat.EntryResult{
				Signal:       config.SignalBuy,
				StrategyName: s.Name(),
				Confluence:   1,
				Tier:         config.TierNormal,
				SLPrice:      mid - m5.ATR*slATRMult,
				TPPrice:      mid + m5.ATR*tpATRMult,
				SLPips:       m5.ATR * slATRMult / b.pipSize,
				TPPips:       m5.ATR * tpATRMult / b.pipSize,
				ATR:          m5.ATR,
			}
			b.forceTestOrder = false
		}

		signalID, err := b.signals.Insert(ctx, signal.Signal{
			SymbolID:            b.symbolUUID,
			Provider:            b.provider.Name(),
			Signal:              result.Signal,
			Reason:              result.Reason,
			Confluence:          result.Confluence,
			Confidence:          result.Confidence,
			ProcessingUS:        time.Since(evalStart).Microseconds(),
			CheckedMarketStates: snapshots,
			BarTime:             &barTime,
			Strategy:            result.StrategyName,
		})
		if err != nil {
			slog.Error("insert signal failed", "strategy", s.Name(), "err", err)
		}

		if result.Signal == config.SignalHold {
			continue
		}

		b.onTradeSignal(ctx, result, b.currentPrice, signalID)
	}
}

func (b *Bot) sameDirLosingPosition(side string) string {
	mid := b.currentPrice.Mid
	if mid == 0 {
		mid = (b.currentPrice.Bid + b.currentPrice.Ask) / 2
	}

	var newest trackedPosition
	found := false
	for _, pos := range b.registry.All() {
		if pos.Side != side || pos.OpenPrice == 0 {
			continue
		}
		if !found || pos.OpenTime.After(newest.OpenTime) {
			newest = pos
			found = true
		}
	}
	if !found {
		return ""
	}

	var lossInPrice float64
	if side == "BUY" {
		lossInPrice = newest.OpenPrice - mid
	} else {
		lossInPrice = mid - newest.OpenPrice
	}
	if lossInPrice <= 0 {
		return ""
	}

	var slDist float64
	if side == "BUY" {
		slDist = newest.OpenPrice - newest.SLPrice
	} else {
		slDist = newest.SLPrice - newest.OpenPrice
	}
	if slDist <= 0 {
		slDist = 5 * b.pipSize
	}

	if lossInPrice > slDist*0.4 {
		return newest.ProviderPositionID
	}
	return ""
}

func (b *Bot) unrealizedUSD(priceDiff float64, volume int64) float64 {
	if b.provider.Name() == "ctrader" {
		return priceDiff * float64(volume) / 100
	}
	return priceDiff * float64(volume) / 100_000_000
}

func (b *Bot) logUnrealizedPnL(currentPrice float64) {
	positions := b.registry.All()
	if len(positions) == 0 {
		return
	}
	var totalUnrealized float64
	for _, pos := range positions {
		if pos.OpenPrice == 0 {
			continue
		}
		var unrealized float64
		if pos.Side == "BUY" {
			unrealized = b.unrealizedUSD(currentPrice-pos.OpenPrice, pos.Volume)
		} else {
			unrealized = b.unrealizedUSD(pos.OpenPrice-currentPrice, pos.Volume)
		}
		totalUnrealized += unrealized
		slog.Info("position P&L",
			"posID", pos.ProviderPositionID,
			"side", pos.Side,
			"openPrice", pos.OpenPrice,
			"currentPrice", currentPrice,
			"unrealizedUSD", fmt.Sprintf("%.2f", unrealized),
			"tier", pos.Tier,
		)
	}
	if len(positions) > 1 {
		slog.Info("total unrealized P&L", "usd", fmt.Sprintf("%.2f", totalUnrealized))
	}
}

func (b *Bot) claimPendingByClientOrderID(clientOrderID string) *pendingOrderState {
	ps, ok := b.pendingOrders[clientOrderID]
	if !ok {
		return nil
	}
	delete(b.pendingOrders, clientOrderID)
	if ps.brokerOrderID != 0 {
		delete(b.brokerOrderIDs, ps.brokerOrderID)
	}
	return ps
}

func (b *Bot) claimPendingByBrokerOrderID(brokerOrderID int64) *pendingOrderState {
	if brokerOrderID == 0 {
		return nil
	}
	clientOrderID, ok := b.brokerOrderIDs[brokerOrderID]
	if !ok {
		return nil
	}
	return b.claimPendingByClientOrderID(clientOrderID)
}

func (b *Bot) onExecution(ctx context.Context, exec provider.ExecutionEvent) {
	if exec.HasDeal && exec.Deal.SymbolID != 0 && b.cfg.CTrader != nil && exec.Deal.SymbolID != b.cfg.CTrader.SymbolID {
		return
	}
	if !exec.HasDeal {
		if exec.Type == config.ExecOrderFilled && exec.ClosedPositionID != "" {
			b.recordBrokerClose(ctx, exec)
			return
		}
		switch exec.Type {
		case config.ExecOrderAccepted:
			slog.Info("order accepted by broker", "clientOrderID", exec.ClientOrderID, "brokerOrderID", exec.BrokerOrderID)
			if exec.BrokerOrderID != 0 && exec.ClientOrderID != "" {
				if ps, ok := b.pendingOrders[exec.ClientOrderID]; ok {
					ps.brokerOrderID = exec.BrokerOrderID
					b.brokerOrderIDs[exec.BrokerOrderID] = exec.ClientOrderID
				}
			}
		case config.ExecOrderReplaced:
			slog.Info("SL/TP amendment confirmed by broker", "brokerOrderID", exec.BrokerOrderID, "posID", exec.PositionID)
			b.signalBreakEvenConfirmed(exec.PositionID)
			return
		case config.ExecOrderCancelled:
			return
		case config.ExecOrderRejected, config.ExecOrderExpired:
			ps := b.claimPendingByClientOrderID(exec.ClientOrderID)
			if ps == nil {
				ps = b.claimPendingByBrokerOrderID(exec.BrokerOrderID)
			}
			if ps == nil {
				return
			}
			slog.Warn("order not filled", "reason", exec.Type, "errorCode", exec.ErrorCode, "orderID", ps.ourOrderID)
			b.orders.UpdateError(ctx, ps.ourOrderID, exec.ErrorCode, exec.Type)
			b.events.Insert(ctx, "order_not_filled", map[string]any{"reason": exec.Type, "errorCode": exec.ErrorCode}, 0)
		}
		return
	}

	switch exec.Type {
	case config.ExecOrderFilled:
		if exec.Deal.IsClose {
			b.recordCloseFill(ctx, exec)
			if exec.Deal.Close != nil && exec.Deal.Close.Balance > 0 {
				b.balanceMu.Lock()
				b.balance = exec.Deal.Close.Balance
				b.balanceMu.Unlock()
				slog.Info("balance updated from close fill", "balance", exec.Deal.Close.Balance)
				go b.provider.SaveBalanceSnapshot(ctx, exec.Deal.Close.Balance)
			} else {
				go b.refreshBalance()
			}
		} else {
			ps := b.claimPendingByClientOrderID(exec.ClientOrderID)
			if ps == nil {
				slog.Warn("open fill for unknown pending order", "clientOrderID", exec.ClientOrderID, "dealID", exec.Deal.DealID)
				return
			}
			b.recordOpenFill(ctx, exec, ps)
		}

	case config.ExecOrderPartialFill:
		slog.Info("partial fill — waiting for full fill",
			"dealID", exec.Deal.DealID,
			"filledVolume", exec.Deal.FilledVolume,
		)

	case config.ExecOrderRejected, config.ExecOrderCancelled, config.ExecOrderExpired:
		ps := b.claimPendingByClientOrderID(exec.ClientOrderID)
		if ps == nil {
			ps = b.claimPendingByBrokerOrderID(exec.BrokerOrderID)
		}
		if ps == nil {
			return
		}
		slog.Warn("order not filled (with deal)", "reason", exec.Type, "errorCode", exec.ErrorCode, "orderID", ps.ourOrderID)
		b.orders.UpdateError(ctx, ps.ourOrderID, exec.ErrorCode, exec.Type)
		b.events.Insert(ctx, "order_not_filled", map[string]any{"reason": exec.Type, "errorCode": exec.ErrorCode}, 0)

	case config.ExecCloseRejected:
		for posID, pc := range b.pendingCloseReasons {
			slog.Warn("close rejected by broker — will retry", "posID", posID, "reason", pc.reason)
			delete(b.pendingCloseReasons, posID)
		}
	}
}

func (b *Bot) onTradeSignal(ctx context.Context, result strat.EntryResult, price provider.PriceEvent, signalID string) {
	b.pausedMu.Lock()
	paused := b.paused
	b.pausedMu.Unlock()
	if paused {
		slog.Info("signal skipped — bot paused")
		return
	}

	if ok, reason := b.registry.CanOpen(result.Tier, result.Signal, result.StrategyName, price.Mid, b.pipSize); !ok {
		slog.Info("signal skipped — position limit", "reason", reason)
		return
	}

	if blockingPosID := b.sameDirLosingPosition(result.Signal); blockingPosID != "" {
		slog.Info("signal skipped — same-direction position already in loss, not adding to loser",
			"side", result.Signal,
			"blockingPosID", blockingPosID,
		)
		return
	}

	if !b.riskMgr.CanTrade(b.getBalance()) {
		slog.Warn("daily loss limit hit — signal skipped",
			"dailyLoss", fmt.Sprintf("$%.2f", b.riskMgr.DailyLoss()),
			"limitPct", fmt.Sprintf("%.0f%%", b.riskMgr.MaxDailyLossPct()),
		)
		b.events.Insert(ctx, "daily_limit_hit", map[string]any{
			"daily_loss": b.riskMgr.DailyLoss(),
		}, 0)
		return
	}

	var volume int64
	if b.provider.Name() == "ctrader" {
		volume = b.lotUnit
	} else {
		var sizeErr error
		volume, sizeErr = b.riskMgr.PositionSizeForTier(b.getBalance(), result.SLPips, result.Tier)
		if sizeErr != nil {
			slog.Warn("position size error", "err", sizeErr)
			return
		}
	}

	if b.provider.Name() == "binance" {
		mid := b.currentPrice.Mid
		if mid == 0 {
			mid = (b.currentPrice.Bid + b.currentPrice.Ask) / 2
		}
		if mid > 0 {
			lev := b.leverage
			if lev <= 0 {
				lev = 1
			}
			maxAffordable := int64((b.getBalance() * lev / mid) * 100_000_000 * 0.80)
			if volume > maxAffordable {
				volume = maxAffordable
			}
		}
		const binanceMinVolume = 100_000
		if volume < binanceMinVolume {
			minUSD := (binanceMinVolume / 100_000_000.0) * mid
			slog.Warn("binance: signal skipped — balance too low to meet minimum order size",
				"balance_usd", b.getBalance(), "min_order_usd", minUSD,
			)
			return
		}
	}

	slPrice := result.SLPrice
	tpPrice := result.TPPrice
	sentAt := time.Now()

	orderID, err := b.orders.Insert(ctx, order.Order{
		SignalID: &signalID,
		Provider: b.provider.Name(),
		SymbolID: b.symbolUUID,
		Side:     result.Signal,
		Volume:   volume,
		SL:       &slPrice,
		TP:       &tpPrice,
		SentAt:   &sentAt,
	})
	if err != nil {
		slog.Error("insert order record failed", "err", err)
	}

	if _, err = b.provider.PlaceMarketOrder(ctx, result.Signal, volume, result.SLPips*b.pipSize, result.TPPips*b.pipSize, orderID); err != nil {
		slog.Error("order failed", "err", err)
		b.orders.UpdateError(ctx, orderID, "SEND_FAILED", err.Error())
		b.events.Insert(ctx, "error", map[string]any{
			"error": err.Error(), "stage": "place_order",
		}, ms(sentAt))
		return
	}

	if b.provider.Name() == "binance" {
		mid := price.Mid
		if mid == 0 {
			mid = (price.Bid + price.Ask) / 2
		}
		b.registry.Register(trackedPosition{
			ProviderPositionID: result.Signal + ":" + orderID,
			Side:               result.Signal,
			Tier:               result.Tier,
			Volume:             volume,
			OpenPrice:          mid,
			SLPrice:            result.SLPrice,
			TPPrice:            result.TPPrice,
			ATR:                result.ATR,
			OpenTime:           sentAt,
			StrategyName:       result.StrategyName,
		})
	} else {
		b.pendingOrders[orderID] = &pendingOrderState{
			ourOrderID:   orderID,
			sentAt:       sentAt,
			side:         result.Signal,
			tier:         result.Tier,
			slPrice:      result.SLPrice,
			tpPrice:      result.TPPrice,
			atr:          result.ATR,
			strategyName: result.StrategyName,
		}
	}

	slog.Info("order sent",
		"signal", result.Signal,
		"tier", result.Tier,
		"confluence", result.Confluence,
		"volume", volume,
		"slPips", fmt.Sprintf("%.1f", result.SLPips),
		"tpPips", fmt.Sprintf("%.1f", result.TPPips),
	)
	b.events.Insert(ctx, "order_sent", map[string]any{
		"order_id":   orderID,
		"signal_id":  signalID,
		"side":       result.Signal,
		"tier":       result.Tier,
		"confluence": result.Confluence,
		"volume":     volume,
	}, ms(sentAt))
}

func (b *Bot) recordOpenFill(ctx context.Context, exec provider.ExecutionEvent, ps *pendingOrderState) {
	if !exec.HasDeal {
		return
	}
	deal := exec.Deal
	roundTripMs := time.Since(ps.sentAt).Milliseconds()
	provOrderID := fmt.Sprintf("%d", deal.OrderID)
	provPosID := fmt.Sprintf("%d", deal.PositionID)

	if err := b.orders.UpdateExecution(ctx,
		ps.ourOrderID, provOrderID, provPosID,
		deal.ExecutionPrice, 0, "filled",
		exec.Timestamp, roundTripMs,
	); err != nil {
		slog.Error("orders.UpdateExecution failed", "err", err)
	}

	openTime := deal.ExecTime
	decisionParamsJSON, err := json.Marshal(decisionParams())
	if err != nil {
		slog.Error("marshal decisionParams failed", "err", err)
	}
	posUUID, err := b.positions.Upsert(ctx, position.Position{
		OurOrderID:         &ps.ourOrderID,
		Provider:           b.provider.Name(),
		ProviderPositionID: provPosID,
		ProviderAcctID:     b.providerAcctID,
		SymbolID:           b.symbolUUID,
		Side:               ps.side,
		Volume:             deal.FilledVolume,
		Tier:               ps.tier,
		OpenPrice:          &deal.ExecutionPrice,
		CurrentSL:          &ps.slPrice,
		CurrentTP:          &ps.tpPrice,
		Status:             "open",
		DecisionParams:     decisionParamsJSON,
		OpenTimestamp:      &openTime,
	})
	if err != nil {
		slog.Error("positions.Upsert (open) failed", "err", err)
	}

	b.registry.Register(trackedPosition{
		ProviderPositionID: provPosID,
		Side:               ps.side,
		Tier:               ps.tier,
		Volume:             deal.FilledVolume,
		OpenPrice:          deal.ExecutionPrice,
		SLPrice:            ps.slPrice,
		TPPrice:            ps.tpPrice,
		ATR:                ps.atr,
		OpenTime:           deal.ExecTime,
		StrategyName:       ps.strategyName,
	})

	volume := deal.Volume
	filledVolume := deal.FilledVolume
	commission := deal.Commission
	openFill := fill.Fill{
		OurOrderID:         &ps.ourOrderID,
		Provider:           b.provider.Name(),
		ProviderFillID:     fmt.Sprintf("%d", deal.DealID),
		ProviderOrderID:    &provOrderID,
		ProviderPositionID: &provPosID,
		SymbolID:           b.symbolUUID,
		Side:               ps.side,
		Volume:             &volume,
		FilledVolume:       &filledVolume,
		ExecutionPrice:     &deal.ExecutionPrice,
		EventType:          "open",
		Commission:         &commission,
		ProviderCreateTime: &deal.CreateTime,
		ProviderExecTime:   &deal.ExecTime,
		ReceivedAt:         exec.Timestamp,
	}
	if posUUID != "" {
		openFill.OurPositionID = &posUUID
	}
	if err := b.fills.Insert(ctx, openFill); err != nil {
		slog.Error("fills.Insert (open) failed", "err", err)
	}

	slog.Info("position opened",
		"posID", provPosID, "side", ps.side,
		"price", deal.ExecutionPrice, "tier", ps.tier,
	)

	if b.dispatcher != nil {
		ep := deal.ExecutionPrice
		slPips := math.Abs(ep-ps.slPrice) / b.pipSize
		tpPips := math.Abs(ps.tpPrice-ep) / b.pipSize
		go b.dispatcher.Dispatch(ctx, notify.EventTradeOpened, notify.TradeOpenedPayload{
			PositionID: provPosID,
			Symbol:     b.symbol,
			Side:       ps.side,
			Price:      ep,
			SLPrice:    ps.slPrice,
			TPPrice:    ps.tpPrice,
			SLPips:     slPips,
			TPPips:     tpPips,
			Strategy:   ps.strategyName,
			Volume:     filledVolume,
		})
	}

	b.events.Insert(ctx, "position_opened", map[string]any{
		"deal_id":     deal.DealID,
		"position_id": provPosID,
		"price":       deal.ExecutionPrice,
		"tier":        ps.tier,
	}, 0)

	if b.testCloseMode.CompareAndSwap(true, false) {
		if pos, ok := b.registry.Get(provPosID); ok {
			slog.Info("TESTCLOSE: closing immediately after fill",
				"posID", provPosID, "volume", pos.Volume)
			b.closeTrackedPosition(ctx, pos, "test_close_cmd")
		}
	}

	if b.testAmendMode.CompareAndSwap(true, false) {
		if pos, ok := b.registry.Get(provPosID); ok {
			go b.runTestAmend(ctx, pos)
		}
	}
}

func (b *Bot) recordCloseFill(ctx context.Context, exec provider.ExecutionEvent) {
	if !exec.HasDeal || !exec.Deal.IsClose {
		return
	}
	deal := exec.Deal
	provPosID := fmt.Sprintf("%d", deal.PositionID)

	tracked, ok := b.registry.Get(provPosID)
	if !ok {
		return // close fill for a position we didn't open — another bot's trade
	}

	cl := deal.Close
	provOrderID := fmt.Sprintf("%d", deal.OrderID)

	var maxFav, maxAdv *float64
	var closeReason *string
	openTime := tracked.OpenTime
	maxFav = &tracked.MaxFavorable
	maxAdv = &tracked.MaxAdverse
	if pc, ok := b.pendingCloseReasons[provPosID]; ok {
		closeReason = &pc.reason
		delete(b.pendingCloseReasons, provPosID)
	} else {
		r := b.classifyBrokerClose(tracked, deal.ExecutionPrice)
		closeReason = &r
	}
	b.registry.Remove(provPosID)
	b.notifyStrategyClosed(tracked.StrategyName, tracked.Side, *closeReason, deal.ExecTime)

	if err := b.positions.Close(ctx, b.provider.Name(), provPosID, deal.ExecTime, maxFav, maxAdv); err != nil {
		slog.Error("positions.Close failed", "err", err)
	}

	posUUID, _ := b.positions.IDByProviderPositionID(ctx, b.provider.Name(), provPosID)

	var durationMs *int64
	if !openTime.IsZero() {
		d := deal.ExecTime.Sub(openTime).Milliseconds()
		durationMs = &d
	}

	closeSide := "SELL"
	if deal.TradeSide == 1 {
		closeSide = "BUY"
	}
	volume := deal.Volume
	filledVolume := deal.FilledVolume
	closedVolume := cl.ClosedVolume
	entryPrice := cl.EntryPrice
	grossProfit := cl.GrossProfit
	swap := cl.Swap
	closeCommission := cl.Commission
	balanceAfter := cl.Balance
	pnlFee := cl.PnLConversionFee
	dealCommission := deal.Commission

	closeFill := fill.Fill{
		Provider:           b.provider.Name(),
		ProviderFillID:     fmt.Sprintf("%d", deal.DealID),
		ProviderOrderID:    &provOrderID,
		ProviderPositionID: &provPosID,
		SymbolID:           b.symbolUUID,
		Side:               closeSide,
		Volume:             &volume,
		FilledVolume:       &filledVolume,
		ExecutionPrice:     &deal.ExecutionPrice,
		EventType:          "close",
		Commission:         &dealCommission,
		CloseEntryPrice:    &entryPrice,
		GrossProfit:        &grossProfit,
		CloseSwap:          &swap,
		CloseCommission:    &closeCommission,
		BalanceAfter:       &balanceAfter,
		ClosedVolume:       &closedVolume,
		PnLConversionFee:   &pnlFee,
		TradeDurationMs:    durationMs,
		CloseReason:        closeReason,
		ProviderCreateTime: &deal.CreateTime,
		ProviderExecTime:   &deal.ExecTime,
		ReceivedAt:         exec.Timestamp,
	}
	if posUUID != "" {
		closeFill.OurPositionID = &posUUID
	}
	if err := b.fills.Insert(ctx, closeFill); err != nil {
		slog.Error("fills.Insert (close) failed", "err", err)
	}

	realized := cl.GrossProfit + cl.Commission + cl.Swap
	isWin := realized > 0
	if err := b.pnls.Upsert(ctx, b.symbolUUID, realized, cl.GrossProfit, cl.Commission, cl.Swap, isWin, 0, 0); err != nil {
		slog.Error("pnls.Upsert failed", "err", err)
	}

	b.riskMgr.RecordTrade(realized)

	slog.Info("position closed",
		"posID", provPosID,
		"grossProfit", cl.GrossProfit,
		"realized", realized,
	)

	if b.dispatcher != nil {
		var dur time.Duration
		if durationMs != nil {
			dur = time.Duration(*durationMs) * time.Millisecond
		}
		go b.dispatcher.Dispatch(ctx, notify.EventTradeClosed, notify.TradeClosedPayload{
			PositionID:  provPosID,
			Symbol:      b.symbol,
			Side:        tracked.Side,
			EntryPrice:  cl.EntryPrice,
			ClosePrice:  deal.ExecutionPrice,
			Realized:    realized,
			IsWin:       isWin,
			Duration:    dur,
			Volume:      closedVolume,
			CloseReason: *closeReason,
		})
	}

	b.events.Insert(ctx, "position_closed", map[string]any{
		"deal_id":      deal.DealID,
		"gross_profit": cl.GrossProfit,
		"balance":      cl.Balance,
	}, 0)
}

func (b *Bot) recordBrokerClose(ctx context.Context, exec provider.ExecutionEvent) {
	posID := exec.ClosedPositionID

	tracked, hasTracked := b.registry.Get(posID)
	if !hasTracked {
		return // broker-close for a position we didn't open — another bot's trade
	}
	b.registry.Remove(posID)

	var closeReason string
	if pc, ok := b.pendingCloseReasons[posID]; ok {
		closeReason = pc.reason
		delete(b.pendingCloseReasons, posID)
	}

	if err := b.positions.Close(ctx, b.provider.Name(), posID, exec.Timestamp, nil, nil); err != nil {
		slog.Error("recordBrokerClose: positions.Close failed", "posID", posID, "err", err)
	}

	posUUID, _ := b.positions.IDByProviderPositionID(ctx, b.provider.Name(), posID)

	mid := b.currentPrice.Mid
	if mid == 0 {
		mid = (b.currentPrice.Bid + b.currentPrice.Ask) / 2
	}

	var estimatedPnL float64
	var durationMs *int64
	closeSide := "SELL"
	if hasTracked {
		if tracked.Side == "BUY" {
			estimatedPnL = b.unrealizedUSD(mid-tracked.OpenPrice, tracked.Volume)
			closeSide = "SELL"
		} else {
			estimatedPnL = b.unrealizedUSD(tracked.OpenPrice-mid, tracked.Volume)
			closeSide = "BUY"
		}
		if closeReason == "" {
			closeReason = b.classifyBrokerClose(tracked, mid)
		}
		if !tracked.OpenTime.IsZero() {
			d := exec.Timestamp.Sub(tracked.OpenTime).Milliseconds()
			durationMs = &d
		}
		b.notifyStrategyClosed(tracked.StrategyName, tracked.Side, closeReason, exec.Timestamp)
	}

	fillID := fmt.Sprintf("broker_%s_%d", posID, exec.Timestamp.UnixMilli())
	brokerFill := fill.Fill{
		Provider:           b.provider.Name(),
		ProviderFillID:     fillID,
		ProviderPositionID: &posID,
		SymbolID:           b.symbolUUID,
		Side:               closeSide,
		ExecutionPrice:     &mid,
		EventType:          "close",
		CloseReason:        &closeReason,
		GrossProfit:        &estimatedPnL,
		TradeDurationMs:    durationMs,
		ReceivedAt:         exec.Timestamp,
	}
	if posUUID != "" {
		brokerFill.OurPositionID = &posUUID
	}
	if err := b.fills.Insert(ctx, brokerFill); err != nil {
		slog.Error("recordBrokerClose: fills.Insert failed", "posID", posID, "err", err)
	}

	isWin := estimatedPnL > 0
	if err := b.pnls.Upsert(ctx, b.symbolUUID, estimatedPnL, estimatedPnL, 0, 0, isWin, 0, 0); err != nil {
		slog.Error("recordBrokerClose: pnls.Upsert failed", "posID", posID, "err", err)
	}

	b.riskMgr.RecordTrade(estimatedPnL)

	go b.refreshBalance()

	slog.Warn("broker closed position without deal — financials estimated from current price",
		"posID", posID,
		"closeSide", closeSide,
		"execPrice", mid,
		"estimatedPnL", fmt.Sprintf("%.2f", estimatedPnL),
		"closeReason", closeReason,
	)
	b.events.Insert(ctx, "broker_close_no_deal", map[string]any{
		"position_id":   posID,
		"estimated_pnl": estimatedPnL,
		"close_reason":  closeReason,
		"exec_price":    mid,
	}, 0)
}

func (b *Bot) storeCandle(ctx context.Context, c provider.Candle) {
	if err := b.candles.Upsert(ctx, candle.Candle{
		SymbolID:   b.symbolUUID,
		Period:     c.Timeframe,
		Open:       c.Open,
		High:       c.High,
		Low:        c.Low,
		Close:      c.Close,
		TickVolume: c.Volume,
		BarTime:    time.Unix(c.OpenTime, 0).UTC(),
		ReceivedAt: time.Now(),
	}); err != nil {
		slog.Error("store candle failed", "period", c.Timeframe, "err", err)
	}
}

func (b *Bot) onTick(ctx context.Context, price provider.PriceEvent) {
	b.currentPrice = price
	if b.registry.Count() > 0 && time.Since(b.lastDrawbackCheck) >= time.Second {
		b.lastDrawbackCheck = time.Now()
		mid := price.Mid
		if price.Bid > 0 && price.Ask > 0 {
			mid = (price.Bid + price.Ask) / 2
		}
		b.checkPeakDrawback(ctx, mid)
	}

	if time.Since(b.lastTickSaved) < time.Second {
		return
	}
	b.lastTickSaved = time.Now()
	t := tick.Tick{
		SymbolID:     b.symbolUUID,
		Bid:          price.Bid,
		Ask:          price.Ask,
		ReceivedAt:   price.Timestamp,
		ProcessingUS: time.Since(price.Timestamp).Microseconds(),
	}
	select {
	case b.tickCh <- t:
	default:
	}
}

func (b *Bot) tickWriter(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-b.tickCh:
			dbCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := b.ticks.Insert(dbCtx, t)
			cancel()
			if err != nil {
				slog.Error("tick insert failed", "err", err)
			}
		}
	}
}

func (b *Bot) Reset() {
	b.pendingOrders = make(map[string]*pendingOrderState)
	b.brokerOrderIDs = make(map[int64]string)
	b.pendingCloseReasons = make(map[string]pendingClose)
	b.lastCandleOpenTime = 0
	b.lastCandleClose = 0
}

func (b *Bot) refreshBalance() {
	info, err := b.provider.FetchAccountInfo(context.Background())
	if err != nil {
		slog.Error("balance refresh failed", "err", err)
		return
	}
	b.balanceMu.Lock()
	b.balance = info.Balance
	b.balanceMu.Unlock()
	slog.Info("balance refreshed", "balance", info.Balance)
}

func (b *Bot) getBalance() float64 {
	b.balanceMu.Lock()
	defer b.balanceMu.Unlock()
	return b.balance
}

func (b *Bot) tokenRefresher(ctx context.Context) {
	ticker := time.NewTicker(55 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := b.provider.RefreshCredentials(ctx); err != nil {
				slog.Error("credentials refresh failed — exiting for systemd restart", "err", err, "provider", b.provider.Name())
				os.Exit(1)
			}
			slog.Info("credentials refreshed", "provider", b.provider.Name())
		}
	}
}

func (b *Bot) weekendPositionCloser(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			utc := t.UTC()
			if utc.Weekday() != time.Friday {
				continue
			}
			if utc.Hour() != 21 || utc.Minute() != 30 {
				continue
			}
			if b.registry.Count() == 0 {
				continue
			}
			slog.Warn("Friday 21:30 UTC — closing all positions before market close")
			for _, pos := range b.registry.All() {
				b.closeTrackedPosition(ctx, pos, "weekend_close")
			}
			b.events.Insert(ctx, "weekend_close", map[string]any{
				"reason": "forex market closes at 22:00 UTC",
			}, 0)
		}
	}
}

func buildMarketStateSnapshots(states map[string]indicator.MarketState) map[string]signal.MarketStateSnapshot {
	out := make(map[string]signal.MarketStateSnapshot, len(states))
	for period, ms := range states {
		if !ms.IsWarmedUp || ms.ID == "" {
			continue
		}
		out[period] = signal.MarketStateSnapshot{MarketStateID: ms.ID}
	}
	return out
}

func SaveCredential(ctx context.Context, db *pgxpool.Pool, key, value string) error {
	_, err := db.Exec(ctx, `
		INSERT INTO bot_credentials (key, value)
		VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = $2, updated_at = NOW()
	`, key, value)
	return err
}

func LoadCredential(ctx context.Context, db *pgxpool.Pool, key string) (string, error) {
	var value string
	err := db.QueryRow(ctx, "SELECT value FROM bot_credentials WHERE key = $1", key).Scan(&value)
	return value, err
}

func ms(t time.Time) int64 {
	return time.Since(t).Milliseconds()
}

func (b *Bot) sendTestPosition(ctx context.Context) {
	testVolume := b.lotUnit
	const (
		testSLPips float64 = 10.0
		testTPPips float64 = 20.0
	)

	slog.Warn("DEV: sending test BUY position",
		"provider", b.provider.Name(),
		"symbol", b.symbol,
		"volume", testVolume,
		"slPips", testSLPips,
		"tpPips", testTPPips,
	)

	sentAt := time.Now()
	orderID, err := b.orders.Insert(ctx, order.Order{
		Provider: b.provider.Name(),
		SymbolID: b.symbolUUID,
		Side:     "BUY",
		Volume:   testVolume,
		SentAt:   &sentAt,
	})
	if err != nil {
		slog.Error("DEV: test order record insert failed", "err", err)
	}

	if _, err := b.provider.PlaceMarketOrder(ctx, config.SignalBuy, testVolume, testSLPips*b.pipSize, testTPPips*b.pipSize, orderID); err != nil {
		slog.Error("DEV: test position placement failed", "provider", b.provider.Name(), "err", err)
		b.orders.UpdateError(ctx, orderID, "SEND_FAILED", err.Error())
		return
	}

	mid := b.currentPrice.Mid
	if mid == 0 {
		mid = (b.currentPrice.Bid + b.currentPrice.Ask) / 2
	}
	b.pendingOrders[orderID] = &pendingOrderState{
		ourOrderID: orderID,
		sentAt:     sentAt,
		side:       config.SignalBuy,
		tier:       config.TierNormal,
		slPrice:    mid - testSLPips*b.pipSize,
		tpPrice:    mid + testTPPips*b.pipSize,
	}

	if b.provider.Name() == "binance" {
		mid := b.currentPrice.Mid
		if mid == 0 {
			mid = (b.currentPrice.Bid + b.currentPrice.Ask) / 2
		}
		b.registry.Register(trackedPosition{
			ProviderPositionID: orderID,
			Side:               "BUY",
			Tier:               config.TierNormal,
			Volume:             testVolume,
			OpenPrice:          mid,
			OpenTime:           sentAt,
		})
		slog.Info("DEV: test position registered (Binance)", "posID", orderID, "openPrice", mid)
	}
}

func (b *Bot) classifyBrokerClose(tracked trackedPosition, execPrice float64) string {
	tol := 5 * b.pipSize

	if tracked.BreakEvenActive {
		var beSL float64
		if tracked.Side == config.SignalBuy {
			beSL = tracked.OpenPrice + 2*b.pipSize
		} else {
			beSL = tracked.OpenPrice - 2*b.pipSize
		}
		if math.Abs(execPrice-beSL) <= tol {
			return strat.CloseReasonBreakevenSL
		}
	}

	if tracked.SLPrice > 0 && math.Abs(execPrice-tracked.SLPrice) <= tol {
		return strat.CloseReasonSLHit
	}

	if tracked.TPPrice > 0 && math.Abs(execPrice-tracked.TPPrice) <= tol {
		return strat.CloseReasonTPHit
	}

	// Fallback: direction from open price
	if tracked.Side == config.SignalBuy && execPrice > tracked.OpenPrice {
		return strat.CloseReasonTPHit
	}
	if tracked.Side == config.SignalSell && execPrice < tracked.OpenPrice {
		return strat.CloseReasonTPHit
	}
	return strat.CloseReasonSLHit
}

func (b *Bot) notifyStrategyClosed(strategyName, side, closeReason string, closeTime time.Time) {
	for _, s := range b.strategies {
		if s.Name() != strategyName {
			continue
		}
		if oa, ok := s.(strat.OutcomeAware); ok {
			oa.OnClosed(side, closeReason, closeTime)
		}
		return
	}
}

func (b *Bot) Pause() {
	b.pausedMu.Lock()
	b.paused = true
	b.pausedMu.Unlock()
}

func (b *Bot) Resume() {
	b.pausedMu.Lock()
	b.paused = false
	b.pausedMu.Unlock()
}

func (b *Bot) IsPaused() bool {
	b.pausedMu.Lock()
	defer b.pausedMu.Unlock()
	return b.paused
}

func (b *Bot) Symbol() string { return b.symbol }

func (b *Bot) TriggerTestClose() string {
	if b.testCloseMode.Load() {
		return fmt.Sprintf("[%s] test close already in progress", b.symbol)
	}
	select {
	case b.testCloseTrigger <- struct{}{}:
		return fmt.Sprintf("[%s] test triggered — opening micro lot and closing immediately. Watch journalctl.", b.symbol)
	default:
		return fmt.Sprintf("[%s] trigger channel busy", b.symbol)
	}
}

func (b *Bot) TriggerTestAmend() string {
	if b.testAmendMode.Load() {
		return fmt.Sprintf("[%s] test amend already in progress", b.symbol)
	}
	select {
	case b.testAmendTrigger <- struct{}{}:
		return fmt.Sprintf("[%s] test triggered — opening micro lot, amending SL, watching for broker confirmation. Watch journalctl.", b.symbol)
	default:
		return fmt.Sprintf("[%s] trigger channel busy", b.symbol)
	}
}

func (b *Bot) StatusText(ctx context.Context) string {
	mode := "Running"
	if b.IsPaused() {
		mode = "Paused"
	}
	positions := b.registry.All()
	pnlData, _ := b.pnls.TodayFull(ctx, b.symbolUUID)
	pnlLine := "Today: no trades"
	if pnlData != nil && pnlData.TradeCount > 0 {
		sign := "+"
		if pnlData.RealizedPnL < 0 {
			sign = ""
		}
		pnlLine = fmt.Sprintf("Today: %d trades (%dW/%dL) %s$%.2f",
			pnlData.TradeCount, pnlData.WinCount, pnlData.LossCount,
			sign, pnlData.RealizedPnL)
	}
	return fmt.Sprintf(
		"<b>%s</b>\nMode: %s\nOpen positions: %d\n%s\nBalance: $%.2f",
		b.symbol, mode, len(positions), pnlLine, b.getBalance(),
	)
}

func (b *Bot) TodayText(ctx context.Context) string {
	pnlData, _ := b.pnls.TodayFull(ctx, b.symbolUUID)
	if pnlData == nil || pnlData.TradeCount == 0 {
		return fmt.Sprintf("<b>%s</b>\nNo trades today.", b.symbol)
	}
	sign := "+"
	if pnlData.RealizedPnL < 0 {
		sign = ""
	}
	return fmt.Sprintf(
		"<b>%s — today</b>\nTrades: %d (%dW / %dL)\nP&amp;L: %s$%.2f\nBalance: $%.2f",
		b.symbol,
		pnlData.TradeCount, pnlData.WinCount, pnlData.LossCount,
		sign, pnlData.RealizedPnL,
		b.getBalance(),
	)
}

func (b *Bot) dailySummarySender(ctx context.Context) {
	if b.dispatcher == nil {
		return
	}
	for {
		now := time.Now().UTC()
		next := time.Date(now.Year(), now.Month(), now.Day(), 22, 0, 0, 0, time.UTC)
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}

		pnlData, err := b.pnls.TodayFull(ctx, b.symbolUUID)
		if err != nil || pnlData == nil {
			continue
		}
		b.dispatcher.Dispatch(ctx, notify.EventDailySummary, notify.DailySummaryPayload{
			Symbol:     b.symbol,
			TradeCount: pnlData.TradeCount,
			WinCount:   pnlData.WinCount,
			LossCount:  pnlData.LossCount,
			Realized:   pnlData.RealizedPnL,
			Balance:    b.getBalance(),
		})
	}
}

func (b *Bot) botWatchDog(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			now := time.Now().UTC()
			if now.Weekday() == time.Saturday ||
				(now.Weekday() == time.Sunday && now.Hour() < 22) ||
				(now.Weekday() == time.Friday && now.Hour() >= 21) {
				continue
			}

			var lastSignalTime time.Time
			b.db.QueryRow(ctx, "SELECT MAX(created_at) FROM signals").Scan(&lastSignalTime)

			if time.Since(lastSignalTime) > 30*time.Minute {
				slog.Error("botWatchDog: no signals received during market hours in the last 30 minutes — exiting for systemd restart", "lastSignalTime", lastSignalTime)
				os.Exit(1)
			}

		}
	}
}
