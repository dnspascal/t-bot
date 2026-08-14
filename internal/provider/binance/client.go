package binance

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	baseURL        = "https://fapi.binance.com"
	testnetBaseURL = "https://testnet.binancefuture.com"
)

type RestClient struct {
	apiKey     string
	apiSecret  string
	baseURL    string
	httpClient *http.Client
}

// AccountResponse from Binance USD-M Futures API (/fapi/v2/account)
type AccountResponse struct {
	CanTrade    bool `json:"canTrade"`
	CanDeposit  bool `json:"canDeposit"`
	CanWithdraw bool `json:"canWithdraw"`
	Assets      []struct {
		Asset            string `json:"asset"`
		WalletBalance    string `json:"walletBalance"`
		UnrealizedProfit string `json:"unrealizedProfit"`
		MarginBalance    string `json:"marginBalance"`
		AvailableBalance string `json:"availableBalance"`
	} `json:"assets"`
	Positions []struct {
		Symbol           string `json:"symbol"`
		PositionAmt      string `json:"positionAmt"`
		EntryPrice       string `json:"entryPrice"`
		UnrealizedProfit string `json:"unrealizedProfit"`
		PositionSide     string `json:"positionSide"`
	} `json:"positions"`
}

// OpenOrderResponse from Binance API
type OpenOrderResponse struct {
	Symbol              string `json:"symbol"`
	OrderID             int64  `json:"orderId"`
	ClientOrderID       string `json:"clientOrderId"`
	Price               string `json:"price"`
	OrigQty             string `json:"origQty"`
	ExecutedQty         string `json:"executedQty"`
	CummulativeQuoteQty string `json:"cummulativeQuoteQty"`
	Status              string `json:"status"`
	TimeInForce         string `json:"timeInForce"`
	Type                string `json:"type"`
	Side                string `json:"side"`
	StopPrice           string `json:"stopPrice"`
	Time                int64  `json:"time"`
	UpdateTime          int64  `json:"updateTime"`
}

type KlineResponse struct {
	OpenTime      int64
	Open          string
	High          string
	Low           string
	Close         string
	Volume        string
	CloseTime     int64
	QuoteVolume   string
	Trades        int64
	TakerBuyBase  string
	TakerBuyQuote string
}

// NewRestClient creates a new Binance REST API client
func NewRestClient(apiKey, apiSecret string, testnet bool) *RestClient {
	baseURL := baseURL
	if testnet {
		baseURL = testnetBaseURL
	}

	// Disable HTTP/2 to avoid runtime panics in Go's HTTP/2 client
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   false, // Disable HTTP/2, use HTTP/1.1 only
	}

	return &RestClient{
		apiKey:    apiKey,
		apiSecret: apiSecret,
		baseURL:   baseURL,
		httpClient: &http.Client{
			Timeout:   10 * time.Second,
			Transport: transport,
		},
	}
}

func (c *RestClient) GetAccount(useServerTime bool) (*AccountResponse, error) {
	params := url.Values{}
	if useServerTime {
		ts, err := c.getServerTime()
		if err != nil {
			return nil, err
		}
		params.Add("timestamp", fmt.Sprintf("%d", ts))
	} else {
		params.Add("timestamp", fmt.Sprintf("%d", time.Now().UnixMilli()))
	}

	path := "/fapi/v2/account"
	resp, err := c.doSignedRequest("GET", path, params)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GetAccount failed: %d - %s", resp.StatusCode, string(body))
	}

	bodyBytes, _ := io.ReadAll(resp.Body)

	var account AccountResponse
	if err := json.Unmarshal(bodyBytes, &account); err != nil {
		return nil, fmt.Errorf("decode account response: %w", err)
	}

	return &account, nil
}

// PlaceMarketOrder places a market order
func (c *RestClient) PlaceMarketOrder(symbol, side string, quantity float64) (orderID string, err error) {
	params := url.Values{}
	params.Add("symbol", symbol)
	params.Add("side", side)
	params.Add("type", "MARKET")
	params.Add("quantity", fmt.Sprintf("%.3f", quantity))
	params.Add("timestamp", fmt.Sprintf("%d", time.Now().UnixMilli()))

	path := "/fapi/v1/order"
	resp, err := c.doSignedRequest("POST", path, params)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("PlaceMarketOrder failed: %d - %s", resp.StatusCode, string(body))
	}

	var orderResp struct {
		OrderID int64 `json:"orderId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&orderResp); err != nil {
		return "", fmt.Errorf("decode order response: %w", err)
	}

	return fmt.Sprintf("%d", orderResp.OrderID), nil
}

// PlaceReduceOnlyOrder closes an existing futures position without opening a new one.
func (c *RestClient) PlaceReduceOnlyOrder(symbol, side string, quantity float64) (string, error) {
	params := url.Values{}
	params.Add("symbol", symbol)
	params.Add("side", side)
	params.Add("type", "MARKET")
	params.Add("quantity", fmt.Sprintf("%.3f", quantity))
	params.Add("reduceOnly", "true")
	params.Add("timestamp", fmt.Sprintf("%d", time.Now().UnixMilli()))

	path := "/fapi/v1/order"
	resp, err := c.doSignedRequest("POST", path, params)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("PlaceReduceOnlyOrder failed: %d - %s", resp.StatusCode, string(body))
	}

	var orderResp struct {
		OrderID int64 `json:"orderId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&orderResp); err != nil {
		return "", fmt.Errorf("decode order response: %w", err)
	}

	return fmt.Sprintf("%d", orderResp.OrderID), nil
}

// PositionRiskResponse represents one entry from /fapi/v2/positionRisk
type PositionRiskResponse struct {
	Symbol           string `json:"symbol"`
	PositionAmt      string `json:"positionAmt"` // negative = short, positive = long, "0" = no position
	EntryPrice       string `json:"entryPrice"`
	UnrealizedProfit string `json:"unrealizedProfit"`
	PositionSide     string `json:"positionSide"` // "BOTH" in one-way mode
	Leverage         string `json:"leverage"`     // e.g. "20"
}

// GetOpenPositions returns futures positions with non-zero positionAmt.
func (c *RestClient) GetOpenPositions(symbol string) ([]PositionRiskResponse, error) {
	params := url.Values{}
	if symbol != "" {
		params.Add("symbol", symbol)
	}
	params.Add("timestamp", fmt.Sprintf("%d", time.Now().UnixMilli()))

	path := "/fapi/v2/positionRisk"
	resp, err := c.doSignedRequest("GET", path, params)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GetOpenPositions failed: %d - %s", resp.StatusCode, string(body))
	}

	var all []PositionRiskResponse
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		return nil, fmt.Errorf("decode positionRisk response: %w", err)
	}

	var open []PositionRiskResponse
	for _, p := range all {
		if p.PositionAmt != "0" && p.PositionAmt != "0.000" {
			open = append(open, p)
		}
	}
	return open, nil
}

// GetLeverage returns the current leverage set for symbol from /fapi/v2/positionRisk.
func (c *RestClient) GetLeverage(symbol string) (float64, error) {
	params := url.Values{}
	params.Add("symbol", symbol)
	params.Add("timestamp", fmt.Sprintf("%d", time.Now().UnixMilli()))

	resp, err := c.doSignedRequest("GET", "/fapi/v2/positionRisk", params)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("GetLeverage failed: %d - %s", resp.StatusCode, string(body))
	}

	var positions []PositionRiskResponse
	if err := json.NewDecoder(resp.Body).Decode(&positions); err != nil {
		return 0, fmt.Errorf("decode positionRisk for leverage: %w", err)
	}

	for _, p := range positions {
		if p.Symbol == symbol && p.Leverage != "" {
			lev, err := strconv.ParseFloat(p.Leverage, 64)
			if err == nil && lev > 0 {
				return lev, nil
			}
		}
	}
	return 0, fmt.Errorf("leverage not found for symbol %s", symbol)
}

// GetOpenOrders fetches open orders for a symbol
func (c *RestClient) GetOpenOrders(symbol string) ([]OpenOrderResponse, error) {
	params := url.Values{}
	if symbol != "" {
		params.Add("symbol", symbol)
	}
	params.Add("timestamp", fmt.Sprintf("%d", time.Now().UnixMilli()))

	path := "/fapi/v1/openOrders"
	resp, err := c.doSignedRequest("GET", path, params)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GetOpenOrders failed: %d - %s", resp.StatusCode, string(body))
	}

	var orders []OpenOrderResponse
	if err := json.NewDecoder(resp.Body).Decode(&orders); err != nil {
		return nil, fmt.Errorf("decode orders response: %w", err)
	}

	return orders, nil
}

func (c *RestClient) GetKlines(symbol, interval string, limit int) ([]KlineResponse, error) {
	params := url.Values{}
	params.Add("symbol", symbol)
	params.Add("interval", interval)
	params.Add("limit", strconv.Itoa(limit))

	path := "/fapi/v1/klines"
	resp, err := c.doRequest("GET", path, params)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GetKlines failed: %d - %s", resp.StatusCode, string(body))
	}

	var rawKlines [][]any
	if err := json.NewDecoder(resp.Body).Decode(&rawKlines); err != nil {
		return nil, fmt.Errorf("decode klines response: %w", err)
	}

	klines := make([]KlineResponse, len(rawKlines))
	for i, raw := range rawKlines {
		if len(raw) < 11 {
			continue
		}

		klines[i] = KlineResponse{
			OpenTime:      int64(raw[0].(float64)),
			Open:          raw[1].(string),
			High:          raw[2].(string),
			Low:           raw[3].(string),
			Close:         raw[4].(string),
			Volume:        raw[5].(string),
			CloseTime:     int64(raw[6].(float64)),
			QuoteVolume:   raw[7].(string),
			Trades:        int64(raw[8].(float64)),
			TakerBuyBase:  raw[9].(string),
			TakerBuyQuote: raw[10].(string),
		}
	}

	return klines, nil
}

func (c *RestClient) ValidateAPIKey() (bool, error) {
	// Step 1: verify we can reach fapi at all.
	pingResp, pingErr := c.doRequest("GET", "/fapi/v1/ping", url.Values{})
	if pingErr != nil {
		return false, fmt.Errorf("fapi ping failed: %w", pingErr)
	}
	pingResp.Body.Close()
	if pingResp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("fapi ping returned %d — cannot reach fapi.binance.com", pingResp.StatusCode)
	}

	// Step 2: fetch server time and sign.
	ts, err := c.getServerTime()
	if err != nil {
		return false, fmt.Errorf("fapi getServerTime failed: %w", err)
	}

	params := url.Values{}
	params.Add("timestamp", fmt.Sprintf("%d", ts))

	path := "/fapi/v2/account"
	resp, err := c.doSignedRequest("GET", path, params)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("binance API rejected: %d - %s", resp.StatusCode, string(body))
	}
	return true, nil
}

func (c *RestClient) doRequest(method, path string, params url.Values) (*http.Response, error) {
	u := fmt.Sprintf("%s%s", c.baseURL, path)

	if method == "GET" {
		if len(params) > 0 {
			u = fmt.Sprintf("%s?%s", u, params.Encode())
		}
		req, err := http.NewRequest(method, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-MBX-APIKEY", c.apiKey)
		return c.httpClient.Do(req)
	}

	req, err := http.NewRequest(method, u, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-MBX-APIKEY", c.apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.httpClient.Do(req)
}

// doSignedRequest signs params and appends the signature last so Binance can verify correctly.
// url.Values.Encode() sorts keys alphabetically — if signature were added to the map, it would
// sort before timestamp, and Binance takes everything before &signature= as the signed payload,
// producing an empty string. Appending manually ensures signature is always the final param.
func (c *RestClient) doSignedRequest(method, path string, params url.Values) (*http.Response, error) {
	encoded := params.Encode()
	sig := c.sign(encoded)
	signed := encoded + "&signature=" + sig

	u := fmt.Sprintf("%s%s", c.baseURL, path)

	if method == "GET" {
		req, err := http.NewRequest(method, u+"?"+signed, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-MBX-APIKEY", c.apiKey)
		return c.httpClient.Do(req)
	}

	req, err := http.NewRequest(method, u, strings.NewReader(signed))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-MBX-APIKEY", c.apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.httpClient.Do(req)
}

func (c *RestClient) sign(data string) string {
	h := hmac.New(sha256.New, []byte(c.apiSecret))
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func (c *RestClient) getServerTime() (int64, error) {
	path := "/fapi/v1/time"
	resp, err := c.doRequest("GET", path, url.Values{})
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var timeResp struct {
		ServerTime int64 `json:"serverTime"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&timeResp); err != nil {
		return 0, err
	}

	return timeResp.ServerTime, nil
}
