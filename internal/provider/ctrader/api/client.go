package api

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type PriceEvent struct {
	Bid       float64
	Ask       float64
	Mid       float64
	Timestamp time.Time
}

type ExecutionEvent struct {
	Type             string
	Deal             DealInfo
	HasDeal          bool
	ClosedPositionID int64
	ErrorCode        string
	Timestamp        time.Time
}

type CtidAccount struct {
	CtidTraderAccountID int64
	IsLive              bool
	TraderLogin         int64
	BrokerName          string
}

type Client struct {
	conn          *Connection
	accountID     int64
	symbolID      int64
	priceDivisor  float64
	absoluteSLTP  bool
	priceDecimals int
	mu            sync.Mutex
	authed        bool
	lastBid       float64
	lastAsk       float64
	spotLogOnce   atomic.Bool
	PriceCh       chan PriceEvent
	ExecutionCh   chan ExecutionEvent
	TrendbarCh    chan Trendbar

	traderResCh     chan TraderInfo
	reconcileResCh  chan []OpenPosition
	trendbarsResCh  chan []Trendbar
	accountListCh   chan []CtidAccount
	dealListResCh   chan []DealInfo
	symbolByIdResCh chan []LightSymbol
	accountAuthedCh chan struct{}
	pendingCloses   atomic.Int32
}

func NewClient(demo bool, accountID, symbolID int64, priceDivisor float64, pipSize float64) *Client {
	priceDecimals := 0
	for v := pipSize / 10; v < 1; v *= 10 {
		priceDecimals++
	}
	c := &Client{
		accountID:       accountID,
		symbolID:        symbolID,
		priceDivisor:    priceDivisor,
		absoluteSLTP:    pipSize >= 0.01,
		priceDecimals:   priceDecimals,
		PriceCh:         make(chan PriceEvent, 100),
		ExecutionCh:     make(chan ExecutionEvent, 10),
		TrendbarCh:      make(chan Trendbar, 10),
		traderResCh:     make(chan TraderInfo, 1),
		reconcileResCh:  make(chan []OpenPosition, 1),
		trendbarsResCh:  make(chan []Trendbar, 1),
		accountListCh:   make(chan []CtidAccount, 1),
		dealListResCh:   make(chan []DealInfo, 1),
		symbolByIdResCh: make(chan []LightSymbol, 1),
		accountAuthedCh: make(chan struct{}),
	}
	c.conn = NewConnection(demo, c.handleMessage)
	return c
}

func (c *Client) Connect() error {
	return c.conn.Connect()
}

func (c *Client) Close() {
	c.conn.Close()
}

func (c *Client) Dead() <-chan struct{} {
	return c.conn.dead
}

func (c *Client) GetAccountList(accessToken string) ([]CtidAccount, error) {
	if err := c.conn.SendRaw(ProtoOAGetAccountListByAccessTokenReq,
		encodeGetAccountListReq(accessToken)); err != nil {
		return nil, fmt.Errorf("GetAccountList send: %w", err)
	}
	select {
	case accounts := <-c.accountListCh:
		return accounts, nil
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("GetAccountList: timeout waiting for response")
	}
}

func (c *Client) SetAccountID(id int64) {
	c.accountID = id
}

func (c *Client) AccountID() int64 {
	return c.accountID
}

func (c *Client) AuthApp(clientID, clientSecret string) error {
	return c.conn.SendRaw(ProtoOAApplicationAuthReq,
		encodeAppAuthReq(clientID, clientSecret))
}

func (c *Client) AuthAccount(accessToken string) error {
	if err := c.conn.SendRaw(ProtoOAAccountAuthReq,
		encodeAccountAuthReq(c.accountID, accessToken)); err != nil {
		return err
	}
	select {
	case <-c.accountAuthedCh:
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("AuthAccount: timeout waiting for server confirmation")
	}
}

func (c *Client) FetchAccountInfo() (TraderInfo, error) {
	if err := c.conn.SendRaw(ProtoOATraderReq, encodeTraderReq(c.accountID)); err != nil {
		return TraderInfo{}, fmt.Errorf("FetchAccountInfo send: %w", err)
	}
	select {
	case info := <-c.traderResCh:
		return info, nil
	case <-time.After(10 * time.Second):
		return TraderInfo{}, fmt.Errorf("FetchAccountInfo: timeout waiting for response")
	}
}

func (c *Client) Reconcile() ([]OpenPosition, error) {
	if err := c.conn.SendRaw(ProtoOAReconcileReq, encodeReconcileReq(c.accountID)); err != nil {
		return nil, fmt.Errorf("Reconcile send: %w", err)
	}
	select {
	case positions := <-c.reconcileResCh:
		return positions, nil
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("Reconcile: timeout waiting for response")
	}
}

func (c *Client) SubscribeSpots() error {
	return c.conn.SendRaw(ProtoOASubscribeSpotsReq,
		encodeSubscribeSpotsReq(c.accountID, c.symbolID))
}

func (c *Client) SubscribeLiveTrendbar(period uint32) error {
	return c.conn.SendRaw(ProtoOASubscribeLiveTrendbarReq,
		encodeSubscribeLiveTrendbarReq(c.accountID, c.symbolID, period))
}

func (c *Client) FetchHistoricalTrendbarsFrom(period uint32, count int, toMs int64) ([]Trendbar, error) {
	if err := c.conn.SendRaw(ProtoOAGetTrendbarsReq,
		encodeGetTrendbarsReq(c.accountID, c.symbolID, period, toMs, uint32(count))); err != nil {
		return nil, fmt.Errorf("FetchHistoricalTrendbarsFrom send: %w", err)
	}
	select {
	case bars := <-c.trendbarsResCh:
		return bars, nil
	case <-time.After(15 * time.Second):
		return nil, fmt.Errorf("FetchHistoricalTrendbarsFrom: timeout")
	}
}

func (c *Client) FetchHistoricalTrendbars(period uint32, count int) ([]Trendbar, error) {
	if err := c.conn.SendRaw(ProtoOAGetTrendbarsReq,
		encodeGetTrendbarsReq(c.accountID, c.symbolID, period, time.Now().UnixMilli(), uint32(count))); err != nil {
		return nil, fmt.Errorf("FetchHistoricalTrendbars send: %w", err)
	}
	select {
	case bars := <-c.trendbarsResCh:
		return bars, nil
	case <-time.After(15 * time.Second):
		return nil, fmt.Errorf("FetchHistoricalTrendbars: timeout waiting for response")
	}
}

func (c *Client) GetDealsForPosition(positionID int64, from time.Time) (*DealInfo, error) {
	fromMs := from.UnixMilli()
	toMs := time.Now().UnixMilli()
	if err := c.conn.SendRaw(ProtoOADealListReq,
		encodeDealListReq(c.accountID, fromMs, toMs, 500)); err != nil {
		return nil, fmt.Errorf("GetDealsForPosition send: %w", err)
	}
	select {
	case deals := <-c.dealListResCh:
		for i := range deals {
			d := &deals[i]
			if d.PositionID == positionID && d.IsClose {
				return d, nil
			}
		}
		return nil, nil
	case <-time.After(15 * time.Second):
		// Drain any late-arriving response so it doesn't corrupt the next call.
		select {
		case <-c.dealListResCh:
		default:
		}
		return nil, fmt.Errorf("GetDealsForPosition: timeout waiting for response")
	}
}

func (c *Client) GetSymbolsByIds(ids []int64) ([]LightSymbol, error) {
	if err := c.conn.SendRaw(ProtoOASymbolsListReq, encodeSymbolByIdReq(c.accountID, ids)); err != nil {
		return nil, fmt.Errorf("GetSymbolsByIds send: %w", err)
	}
	select {
	case syms := <-c.symbolByIdResCh:
		return syms, nil
	case <-time.After(15 * time.Second):
		return nil, fmt.Errorf("GetSymbolsByIds: timeout waiting for response")
	}
}

// ClosePositionVariant sends a close with caller-specified proto field numbers.
// Used by the closetest diagnostic command to try alternative field assignments.
func (c *Client) ClosePositionVariant(positionID, volume int64, posIDField, volField int) error {
	c.mu.Lock()
	authed := c.authed
	accountID := c.accountID
	c.mu.Unlock()
	if !authed {
		return fmt.Errorf("not authenticated")
	}
	inner := EncodeClosePositionReqVariant(accountID, positionID, volume, posIDField, volField)
	slog.Info("closing position (variant)",
		"positionID", positionID,
		"volume", volume,
		"posIDField", posIDField,
		"volField", volField,
		"innerHex", fmt.Sprintf("%x", inner),
	)
	if err := c.conn.SendRaw(ProtoOAClosePositionReq, inner); err != nil {
		return err
	}
	c.pendingCloses.Add(1)
	return nil
}

func (c *Client) ClosePosition(positionID, volume int64) error {
	c.mu.Lock()
	authed := c.authed
	accountID := c.accountID
	c.mu.Unlock()

	if !authed {
		return fmt.Errorf("not authenticated")
	}

	inner := encodeClosePositionReq(accountID, positionID, volume)
	slog.Info("closing position",
		"positionID", positionID,
		"volume", volume,
		"innerHex", fmt.Sprintf("%x", inner),
	)

	if err := c.conn.SendRaw(ProtoOAClosePositionReq, inner); err != nil {
		return err
	}
	c.pendingCloses.Add(1)
	return nil
}

func (c *Client) PlaceMarketOrder(side uint32, volume int64, slDist, tpDist float64) error {
	c.mu.Lock()
	authed := c.authed
	lastBid, lastAsk := c.lastBid, c.lastAsk
	c.mu.Unlock()

	if !authed {
		return fmt.Errorf("not authenticated")
	}

	slog.Info("placing order",
		"side", sideStr(side),
		"volume", volume,
		"slDist", slDist,
		"tpDist", tpDist,
	)
	return c.conn.SendRaw(ProtoOANewOrderReq,
		encodeNewOrderReq(c.accountID, c.symbolID, side, volume, slDist, tpDist,
			c.priceDivisor, c.absoluteSLTP, c.priceDecimals, lastBid, lastAsk))
}

func (c *Client) handleMessage(payloadType uint32, payload []byte) {
	switch payloadType {
	case ProtoOAApplicationAuthRes:
		slog.Info("app authenticated")

	case ProtoOAAccountAuthRes:
		c.mu.Lock()
		c.authed = true
		c.mu.Unlock()
		slog.Info("account authenticated", "accountID", c.accountID)
		select {
		case <-c.accountAuthedCh:
		default:
			close(c.accountAuthedCh)
		}

	case ProtoOASpotEvent:
		symID, bid, ask, ok := decodeSpotEvent(payload)
		if c.spotLogOnce.CompareAndSwap(false, true) {
			slog.Info("first spot event decoded", "symID", symID, "wantSymID", c.symbolID, "ok", ok, "bid", bid)
		}
		if ok && symID != 0 && symID != c.symbolID {
			return // ignore spot events from other symbols subscribed on this account
		}
		if ok {
			bidF := float64(bid) / c.priceDivisor
			askF := float64(ask) / c.priceDivisor
			c.mu.Lock()
			if bidF > 0 {
				c.lastBid = bidF
			}
			if askF > 0 {
				c.lastAsk = askF
			}
			lastBid, lastAsk := c.lastBid, c.lastAsk
			c.mu.Unlock()
			mid := lastBid
			if lastBid > 0 && lastAsk > 0 {
				mid = (lastBid + lastAsk) / 2
			}
			event := PriceEvent{
				Bid:       lastBid,
				Ask:       lastAsk,
				Mid:       mid,
				Timestamp: time.Now().UTC(),
			}
			select {
			case c.PriceCh <- event:
			default:
			}
		}

		for _, bar := range decodeLiveTrendbarEvents(payload) {

			select {
			case c.TrendbarCh <- bar:
			default:
			}
		}

	case ProtoOASubscribeLiveTrendbarRes:

	case ProtoOAGetTrendbarsRes:
		bars := decodeGetTrendbarsRes(payload)
		select {
		case c.trendbarsResCh <- bars:
		default:
		}

	case ProtoOATraderRes:
		if info, ok := decodeTraderRes(payload); ok {
			slog.Info("account info received",
				"balance", info.Balance,
				"leverage", info.Leverage,
				"broker", info.BrokerName,
			)
			select {
			case c.traderResCh <- info:
			default:
			}
		} else {
			slog.Warn("ProtoOATraderRes: no usable trader data in server response")
			select {
			case c.traderResCh <- TraderInfo{}:
			default:
			}
		}

	case ProtoOAReconcileRes:
		positions := decodeReconcileRes(payload)
		select {
		case c.reconcileResCh <- positions:
		default:
		}

	case ProtoOAExecutionEvent:
		execType, deal, hasDeal, closedPosID := decodeFullExecutionEvent(payload)
		slog.Info("execution event received",
			"type", execType,
			"dealID", deal.DealID,
			"positionID", deal.PositionID,
			"closedPosID", closedPosID,
			"executionPrice", deal.ExecutionPrice,
			"isClose", deal.IsClose,
		)

		if hasDeal && deal.IsClose {
			c.pendingCloses.Add(-1)
		}

		select {
		case c.ExecutionCh <- ExecutionEvent{
			Type:             execType,
			Deal:             deal,
			HasDeal:          hasDeal,
			ClosedPositionID: closedPosID,
			Timestamp:        time.Now().UTC(),
		}:
		default:
		}

	case ProtoOAOrderErrorEvent:
		errorCode, orderID, description := decodeOrderError(payload)
		slog.Error("order error event",
			"errorCode", errorCode,
			"orderID", orderID,
			"description", description,
		)
		select {
		case c.ExecutionCh <- ExecutionEvent{
			Type:      "ORDER_REJECTED",
			ErrorCode: errorCode,
			Timestamp: time.Now().UTC(),
		}:
		default:
		}

	case ProtoOADealListRes:
		deals := decodeDealListRes(payload)
		select {
		case c.dealListResCh <- deals:
		default:
		}

	case ProtoOASymbolsListRes: // 2117: on this server, response to by-ID lookup
		syms := decodeSymbolByIdRes(payload)
		select {
		case c.symbolByIdResCh <- syms:
		default:
		}

	case ProtoOAGetAccountListByAccessTokenRes:
		accounts := decodeAccountListRes(payload)
		select {
		case c.accountListCh <- accounts:
		default:
		}

	case ProtoOASubscribeSpotsRes:

	case ProtoOAErrorRes:
		code, desc := decodeOAError(payload)
		slog.Warn("cTrader OA error", "code", code, "description", desc, "rawHex", fmt.Sprintf("%x", payload))
		switch code {
		case "SYMBOL_NOT_FOUND":
			select {
			case c.symbolByIdResCh <- nil:
			default:
			}
		case "INCORRECT_BOUNDARIES":
			pending := c.pendingCloses.Load()
			slog.Warn("INCORRECT_BOUNDARIES received", "pendingCloses", pending)
			if pending > 0 {
				c.pendingCloses.Add(-1)
				select {
				case c.ExecutionCh <- ExecutionEvent{Type: "CLOSE_REJECTED", ErrorCode: "INCORRECT_BOUNDARIES", Timestamp: time.Now().UTC()}:
				default:
				}
			} else {
				select {
				case c.trendbarsResCh <- nil:
				default:
				}
				select {
				case c.dealListResCh <- nil:
				default:
				}
			}
		}

	case 50:
		code, desc := decodeGenericError(payload)
		slog.Error("cTrader protocol error", "code", code, "description", desc)

	case 51:

	default:
		slog.Debug("unhandled message", "payloadType", payloadType, "payloadHex", fmt.Sprintf("%x", payload))
	}
}

func sideStr(side uint32) string {
	if side == TradeSideBuy {
		return "BUY"
	}
	return "SELL"
}
