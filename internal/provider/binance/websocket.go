package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/gorilla/websocket"
)

const (
	wsBaseURL    = "wss://fstream.binance.com"
	wsTestnetURL = "wss://stream.binancefuture.com"

	wsPingInterval = 3 * time.Minute
	wsReadTimeout  = 5 * time.Minute
)

type jsonFloat float64

func (f *jsonFloat) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		v, err := strconv.ParseFloat(string(b[1:len(b)-1]), 64)
		if err != nil {
			return err
		}
		*f = jsonFloat(v)
		return nil
	}
	var v float64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*f = jsonFloat(v)
	return nil
}

type wsEnvelope struct {
	Stream string          `json:"stream"`
	Data   json.RawMessage `json:"data"`
}

type wsBookTicker struct {
	Symbol  string    `json:"s"`
	Bid     jsonFloat `json:"b"`
	BidSize jsonFloat `json:"B"`
	Ask     jsonFloat `json:"a"`
	AskSize jsonFloat `json:"A"`
}

type wsKlineEvent struct {
	Symbol string  `json:"s"`
	Kline  wsKline `json:"k"`
}

type wsKline struct {
	OpenTime int64  `json:"t"`
	Interval string `json:"i"`
	Open     string `json:"o"`
	High     string `json:"h"`
	Low      string `json:"l"`
	Close    string `json:"c"`
	Volume   string `json:"v"`
	IsClosed bool   `json:"x"`
}

type KlineData struct {
	Symbol   string
	Interval string
	OpenTime int64 // unix seconds
	Open     float64
	High     float64
	Low      float64
	Close    float64
	Volume   float64
}

type WebSocketClient struct {
	symbol  string
	baseURL string
	conn    *websocket.Conn
	mu      sync.RWMutex

	priceCh  chan PriceData
	klineCh  chan KlineData
	closedCh chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
}

type PriceData struct {
	Bid       float64
	Ask       float64
	BidSize   float64
	AskSize   float64
	Timestamp time.Time
}

func NewWebSocketClient(symbol string, testnet bool) *WebSocketClient {
	baseURL := wsBaseURL
	if testnet {
		baseURL = wsTestnetURL
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &WebSocketClient{
		symbol:   symbol,
		baseURL:  baseURL,
		priceCh:  make(chan PriceData, 100),
		klineCh:  make(chan KlineData, 50),
		closedCh: make(chan struct{}),
		ctx:      ctx,
		cancel:   cancel,
	}
}

func (w *WebSocketClient) buildURL() string {
	symbol := strings.ToLower(w.symbol)
	// Combined-stream URL subscribes to bookTicker + all kline intervals in one connection.
	// Combined format wraps every message in {"stream":"...","data":{...}}, enabling clean routing.
	streams := []string{symbol + "@bookTicker"}
	for _, interval := range config.BinanceIntervals() {
		streams = append(streams, fmt.Sprintf("%s@kline_%s", symbol, interval))
	}
	return fmt.Sprintf("%s/stream?streams=%s", w.baseURL, strings.Join(streams, "/"))
}

func (w *WebSocketClient) dial() error {
	wsURL := w.buildURL()
	slog.Info("connecting to websocket", "url", wsURL)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("websocket dial: %w", err)
	}
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		return nil
	})
	conn.SetReadDeadline(time.Now().Add(wsReadTimeout))

	w.mu.Lock()
	if w.conn != nil {
		w.conn.Close()
	}
	w.conn = conn
	w.mu.Unlock()
	return nil
}

func (w *WebSocketClient) Connect() error {
	if err := w.dial(); err != nil {
		return err
	}
	slog.Info("websocket connected")
	go w.readLoop()
	go w.keepAlive()
	return nil
}

func (w *WebSocketClient) Close() error {
	w.cancel()
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.conn != nil {
		close(w.closedCh)
		return w.conn.Close()
	}
	return nil
}

func (w *WebSocketClient) PriceChan() <-chan PriceData { return w.priceCh }
func (w *WebSocketClient) KlineChan() <-chan KlineData { return w.klineCh }
func (w *WebSocketClient) ClosedChan() <-chan struct{} { return w.closedCh }

func (w *WebSocketClient) keepAlive() {
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.mu.RLock()
			conn := w.conn
			w.mu.RUnlock()
			if conn == nil {
				continue
			}
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
				slog.Debug("websocket keepalive ping failed", "err", err)
			}
		case <-w.ctx.Done():
			return
		}
	}
}

func (w *WebSocketClient) readLoop() {
	defer close(w.priceCh)
	defer close(w.klineCh)

	backoff := 2 * time.Second
	for {
		err := w.readMessages()
		if w.ctx.Err() != nil {
			slog.Info("websocket context cancelled")
			return
		}
		slog.Warn("websocket disconnected, reconnecting", "err", err, "backoff", backoff)
		select {
		case <-time.After(backoff):
		case <-w.ctx.Done():
			return
		}
		if err := w.dial(); err != nil {
			slog.Error("websocket reconnect failed", "err", err)
			if backoff < 60*time.Second {
				backoff *= 2
			}
			continue
		}
		slog.Info("websocket reconnected")
		backoff = 2 * time.Second
	}
}

func (w *WebSocketClient) readMessages() error {
	for {
		select {
		case <-w.ctx.Done():
			return nil
		default:
		}

		w.mu.RLock()
		conn := w.conn
		w.mu.RUnlock()

		if conn == nil {
			return fmt.Errorf("connection is nil")
		}

		var raw json.RawMessage
		if err := conn.ReadJSON(&raw); err != nil {
			return err
		}
		conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		w.processMessage(raw)
	}
}

func (w *WebSocketClient) processMessage(data json.RawMessage) {
	var env wsEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		slog.Debug("failed to unmarshal envelope", "err", err)
		return
	}

	if strings.Contains(env.Stream, "@bookTicker") {
		w.processBookTicker(env.Data)
	} else if strings.Contains(env.Stream, "@kline_") {
		w.processKline(env.Data)
	}
}

func (w *WebSocketClient) processBookTicker(data json.RawMessage) {
	var t wsBookTicker
	if err := json.Unmarshal(data, &t); err != nil {
		slog.Debug("failed to unmarshal bookTicker", "err", err)
		return
	}

	select {
	case w.priceCh <- PriceData{
		Bid:       float64(t.Bid),
		BidSize:   float64(t.BidSize),
		Ask:       float64(t.Ask),
		AskSize:   float64(t.AskSize),
		Timestamp: time.Now(),
	}:
	default:
		// bookTicker fires hundreds/sec; channel is intentionally throttled — silent drop is expected.
	}
}

func (w *WebSocketClient) processKline(data json.RawMessage) {
	var evt wsKlineEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		slog.Warn("failed to unmarshal kline", "err", err, "raw", string(data))
		return
	}
	slog.Info("kline event", "interval", evt.Kline.Interval, "closed", evt.Kline.IsClosed)
	if !evt.Kline.IsClosed {
		return // only emit completed candles
	}
	slog.Info("kline closed", "interval", evt.Kline.Interval, "open_time", evt.Kline.OpenTime)

	open, _ := strconv.ParseFloat(evt.Kline.Open, 64)
	high, _ := strconv.ParseFloat(evt.Kline.High, 64)
	low, _ := strconv.ParseFloat(evt.Kline.Low, 64)
	close, _ := strconv.ParseFloat(evt.Kline.Close, 64)
	vol, _ := strconv.ParseFloat(evt.Kline.Volume, 64)

	select {
	case w.klineCh <- KlineData{
		Symbol:   evt.Symbol,
		Interval: evt.Kline.Interval,
		OpenTime: evt.Kline.OpenTime / 1000,
		Open:     open,
		High:     high,
		Low:      low,
		Close:    close,
		Volume:   vol,
	}:
	default:
		slog.Warn("kline channel full, dropping closed candle", "interval", evt.Kline.Interval)
	}
}
