package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/denismgaya/t-bot/internal/database"
)

var tz = func() *time.Location {
	loc, err := time.LoadLocation("Africa/Dar_es_Salaam")
	if err != nil {
		log.Fatal("load Africa/Dar_es_Salaam timezone:", err)
	}
	return loc
}()

// startOfTodayTZ returns midnight of the current Dar-es-Salaam calendar day.
func startOfTodayTZ() time.Time {
	now := time.Now().In(tz)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, tz)
}

func main() {
	godotenv.Load()

	apiKey := mustEnv("API_KEY")
	allowedOrigin := getEnv("ALLOWED_ORIGIN", "*")
	port := getEnv("API_PORT", "8090")
	dbURL := mustEnv("DATABASE_URL")

	ctx := context.Background()
	pool, err := database.New(ctx, dbURL, 5, 1)
	if err != nil {
		log.Fatal("db:", err)
	}
	defer pool.Close()

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware(allowedOrigin))
	r.Use(authMiddleware(apiKey))

	h := &handler{db: pool}

	r.Get("/metrics", h.metrics)
	r.Get("/trades", h.trades)
	r.Get("/trades/{id}", h.trade)
	r.Get("/live", h.live)

	slog.Info("api listening", "port", port, "origin", allowedOrigin)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatal(err)
	}
}

type handler struct{ db *pgxpool.Pool }

// ── /metrics?date=YYYY-MM-DD ─────────────────────────────────────────────────

type DailyMetrics struct {
	Date            string  `json:"date"`
	NetPnL          float64 `json:"net_pnl"`
	GrossPnL        float64 `json:"gross_pnl"`
	TotalCommission float64 `json:"total_commission"`
	TradeCount      int     `json:"trade_count"`
	WinCount        int     `json:"win_count"`
	LossCount       int     `json:"loss_count"`
	WinRate         float64 `json:"win_rate"`
	AvgWin          float64 `json:"avg_win"`
	AvgLoss         float64 `json:"avg_loss"`
	LargestWin      float64 `json:"largest_win"`
	LargestLoss     float64 `json:"largest_loss"`
}

func (h *handler) metrics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	date := q.Get("date")
	fromStr := q.Get("from")
	toStr := q.Get("to")
	symbol := q.Get("symbol")

	var start, end time.Time
	var err error

	switch {
	case date != "":
		start, err = time.ParseInLocation("2006-01-02", date, tz)
		if err != nil {
			jsonErr(w, "invalid date", http.StatusBadRequest)
			return
		}
		end = start.AddDate(0, 0, 1)
	case fromStr != "" || toStr != "":
		if fromStr != "" {
			start, err = time.ParseInLocation("2006-01-02", fromStr, tz)
			if err != nil {
				jsonErr(w, "invalid from", http.StatusBadRequest)
				return
			}
		} else {
			start = startOfTodayTZ().AddDate(0, -1, 0)
		}
		if toStr != "" {
			end, err = time.ParseInLocation("2006-01-02", toStr, tz)
			if err != nil {
				jsonErr(w, "invalid to", http.StatusBadRequest)
				return
			}
			end = end.AddDate(0, 0, 1)
		} else {
			end = startOfTodayTZ().AddDate(0, 0, 1)
		}
	default:
		start = startOfTodayTZ()
		end = start.AddDate(0, 0, 1)
	}

	args := []any{start, end}
	symbolFilter := ""
	if symbol != "" {
		args = append(args, symbol)
		symbolFilter = fmt.Sprintf("AND sym.symbol = $%d", len(args))
	}

	row := h.db.QueryRow(r.Context(), `
		SELECT
			COUNT(*)::int                                                     AS trade_count,
			COUNT(*) FILTER (WHERE f.net_profit > 0)::int                    AS win_count,
			COUNT(*) FILTER (WHERE f.net_profit <= 0)::int                   AS loss_count,
			COALESCE(SUM(f.gross_profit), 0)                                 AS gross_pnl,
			COALESCE(SUM(f.net_profit), 0)                                   AS net_pnl,
			COALESCE(SUM(f.close_commission), 0)                             AS total_commission,
			COALESCE(AVG(f.net_profit) FILTER (WHERE f.net_profit > 0), 0)  AS avg_win,
			COALESCE(AVG(f.net_profit) FILTER (WHERE f.net_profit <= 0), 0) AS avg_loss,
			COALESCE(MAX(f.net_profit), 0)                                   AS largest_win,
			COALESCE(MIN(f.net_profit), 0)                                   AS largest_loss
		FROM positions p
		JOIN orders o ON o.id = p.our_order_id
		JOIN symbols sym ON sym.id = p.symbol_id
		JOIN fills f ON f.our_position_id = p.id AND f.close_reason IS NOT NULL
		WHERE p.open_timestamp >= $1 AND p.open_timestamp < $2
		  AND p.provider = 'ctrader'
		  AND o.signal_id IS NOT NULL
		  `+symbolFilter+`
	`, args...)

	var m DailyMetrics
	m.Date = start.Format("2006-01-02")
	if err := row.Scan(
		&m.TradeCount, &m.WinCount, &m.LossCount,
		&m.GrossPnL, &m.NetPnL, &m.TotalCommission,
		&m.AvgWin, &m.AvgLoss, &m.LargestWin, &m.LargestLoss,
	); err != nil {
		jsonErr(w, "query failed", http.StatusInternalServerError)
		slog.Error("metrics query", "err", err)
		return
	}

	if m.TradeCount > 0 {
		m.WinRate = math.Round(float64(m.WinCount)/float64(m.TradeCount)*1000) / 10
	}

	jsonOK(w, m)
}

// ── /trades ──────────────────────────────────────────────────────────────────

type Trade struct {
	ID                 string  `json:"id"`
	ProviderPositionID string  `json:"provider_position_id"`
	Strategy           string  `json:"strategy"`
	Symbol             string  `json:"symbol"`
	Side               string  `json:"side"`
	OpenPrice          float64 `json:"open_price"`
	ClosePrice         float64 `json:"close_price"`
	SLPrice            float64 `json:"sl_price"`
	TPPrice            float64 `json:"tp_price"`
	MaxFavorable       float64 `json:"max_favorable"`
	MaxAdverse         float64 `json:"max_adverse"`
	GrossProfit        float64 `json:"gross_profit"`
	Commission         float64 `json:"commission"`
	NetProfit          float64 `json:"net_profit"`
	CloseReason        string  `json:"close_reason"`
	OpenAt             string  `json:"open_at"`
	CloseAt            string  `json:"close_at"`
	DurationMinutes    int     `json:"duration_minutes"`
	PipsMove           float64 `json:"pips_move"`
	PipsToTP           float64 `json:"pips_to_tp"`
	PipsToSL           float64 `json:"pips_to_sl"`
}

// pipsFields computes the pip-denominated fields for a trade from its raw prices and pip size.
func pipsFields(t *Trade, pipSize float64) {
	if pipSize <= 0 {
		return
	}
	diff := t.ClosePrice - t.OpenPrice
	if t.Side == "SELL" {
		diff = -diff
	}
	t.PipsMove = math.Round(diff/pipSize*10) / 10
	if t.TPPrice > 0 {
		t.PipsToTP = math.Round(math.Abs(t.TPPrice-t.OpenPrice)/pipSize*10) / 10
	}
	if t.SLPrice > 0 {
		t.PipsToSL = math.Round(math.Abs(t.SLPrice-t.OpenPrice)/pipSize*10) / 10
	}
}

type TradesResponse struct {
	Trades   []Trade `json:"trades"`
	Total    int     `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
}

func (h *handler) trades(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	var start, end time.Time
	if date := q.Get("date"); date != "" {
		t, err := time.ParseInLocation("2006-01-02", date, tz)
		if err != nil {
			jsonErr(w, "invalid date", http.StatusBadRequest)
			return
		}
		start, end = t, t.AddDate(0, 0, 1)
	} else {
		if f := q.Get("from"); f != "" {
			t, err := time.ParseInLocation("2006-01-02", f, tz)
			if err != nil {
				jsonErr(w, "invalid from", http.StatusBadRequest)
				return
			}
			start = t
		}
		if t := q.Get("to"); t != "" {
			parsed, err := time.ParseInLocation("2006-01-02", t, tz)
			if err != nil {
				jsonErr(w, "invalid to", http.StatusBadRequest)
				return
			}
			end = parsed.AddDate(0, 0, 1)
		}
		if start.IsZero() {
			start = startOfTodayTZ()
			end = start.AddDate(0, 0, 1)
		}
	}

	symbol := q.Get("symbol")
	args := []any{start, end}
	symbolFilter := ""
	if symbol != "" {
		args = append(args, symbol)
		symbolFilter = fmt.Sprintf("AND sym.symbol = $%d", len(args))
	}

	baseQuery := `
		FROM positions p
		JOIN orders o ON o.id = p.our_order_id
		JOIN signals sig ON sig.id = o.signal_id
		JOIN symbols sym ON sym.id = p.symbol_id
		LEFT JOIN symbol_configs sc ON sc.symbol_id = sym.id AND sc.deleted_at IS NULL
		JOIN fills f ON f.our_position_id = p.id AND f.close_reason IS NOT NULL
		WHERE p.open_timestamp >= $1 AND p.open_timestamp < $2
		  AND p.provider = 'ctrader'
		  ` + symbolFilter + `
	`

	var total int
	if err := h.db.QueryRow(r.Context(), "SELECT COUNT(*) "+baseQuery, args...).Scan(&total); err != nil {
		jsonErr(w, "count failed", http.StatusInternalServerError)
		slog.Error("trades count", "err", err)
		return
	}

	limitArg := len(args) + 1
	offsetArg := len(args) + 2
	queryArgs := append(append([]any{}, args...), pageSize, offset)

	rows, err := h.db.Query(r.Context(), `
		SELECT
			p.id,
			p.provider_position_id,
			sig.strategy,
			sym.symbol,
			p.side,
			COALESCE(p.open_price, 0),
			COALESCE(f.execution_price, 0),
			COALESCE(p.current_sl, 0),
			COALESCE(p.current_tp, 0),
			COALESCE(p.max_favorable, 0),
			COALESCE(p.max_adverse, 0),
			COALESCE(f.gross_profit, 0),
			COALESCE(f.close_commission, 0),
			COALESCE(f.net_profit, 0),
			COALESCE(f.close_reason, ''),
			p.open_timestamp,
			COALESCE(p.close_timestamp, NOW()),
			EXTRACT(EPOCH FROM (COALESCE(p.close_timestamp, NOW()) - p.open_timestamp))::int / 60,
			COALESCE(sc.pip_size, 0)
		`+baseQuery+fmt.Sprintf(`
		ORDER BY p.open_timestamp DESC
		LIMIT $%d OFFSET $%d
	`, limitArg, offsetArg), queryArgs...)
	if err != nil {
		jsonErr(w, "query failed", http.StatusInternalServerError)
		slog.Error("trades query", "err", err)
		return
	}
	defer rows.Close()

	trades := make([]Trade, 0, pageSize)
	for rows.Next() {
		var t Trade
		var openAt, closeAt time.Time
		var pipSize float64
		if err := rows.Scan(
			&t.ID, &t.ProviderPositionID, &t.Strategy, &t.Symbol,
			&t.Side, &t.OpenPrice, &t.ClosePrice, &t.SLPrice, &t.TPPrice,
			&t.MaxFavorable, &t.MaxAdverse,
			&t.GrossProfit, &t.Commission, &t.NetProfit, &t.CloseReason,
			&openAt, &closeAt, &t.DurationMinutes, &pipSize,
		); err != nil {
			slog.Error("trades scan", "err", err)
			continue
		}
		t.OpenAt = openAt.Format(time.RFC3339)
		t.CloseAt = closeAt.Format(time.RFC3339)
		pipsFields(&t, pipSize)
		trades = append(trades, t)
	}

	jsonOK(w, TradesResponse{
		Trades:   trades,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// ── /trades/{id} ─────────────────────────────────────────────────────────────

type Candle struct {
	Time  int64   `json:"time"`
	Open  float64 `json:"open"`
	High  float64 `json:"high"`
	Low   float64 `json:"low"`
	Close float64 `json:"close"`
}

type TradeDetail struct {
	Trade
	Candles []Candle `json:"candles"`
}

func (h *handler) trade(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var t Trade
	var openAt, closeAt time.Time
	var symbolID string
	var pipSize float64

	err := h.db.QueryRow(r.Context(), `
		SELECT
			p.id,
			p.provider_position_id,
			sig.strategy,
			sym.symbol,
			sym.id,
			p.side,
			COALESCE(p.open_price, 0),
			COALESCE(f.execution_price, 0),
			COALESCE(p.current_sl, 0),
			COALESCE(p.current_tp, 0),
			COALESCE(p.max_favorable, 0),
			COALESCE(p.max_adverse, 0),
			COALESCE(f.gross_profit, 0),
			COALESCE(f.close_commission, 0),
			COALESCE(f.net_profit, 0),
			COALESCE(f.close_reason, ''),
			p.open_timestamp,
			COALESCE(p.close_timestamp, NOW()),
			EXTRACT(EPOCH FROM (COALESCE(p.close_timestamp, NOW()) - p.open_timestamp))::int / 60,
			COALESCE(sc.pip_size, 0)
		FROM positions p
		JOIN orders o ON o.id = p.our_order_id
		JOIN signals sig ON sig.id = o.signal_id
		JOIN symbols sym ON sym.id = p.symbol_id
		LEFT JOIN symbol_configs sc ON sc.symbol_id = sym.id AND sc.deleted_at IS NULL
		JOIN fills f ON f.our_position_id = p.id AND f.close_reason IS NOT NULL
		WHERE p.id = $1
	`, id).Scan(
		&t.ID, &t.ProviderPositionID, &t.Strategy, &t.Symbol, &symbolID,
		&t.Side, &t.OpenPrice, &t.ClosePrice, &t.SLPrice, &t.TPPrice,
		&t.MaxFavorable, &t.MaxAdverse,
		&t.GrossProfit, &t.Commission, &t.NetProfit, &t.CloseReason,
		&openAt, &closeAt, &t.DurationMinutes, &pipSize,
	)
	if err != nil {
		jsonErr(w, "not found", http.StatusNotFound)
		return
	}
	t.OpenAt = openAt.Format(time.RFC3339)
	t.CloseAt = closeAt.Format(time.RFC3339)
	pipsFields(&t, pipSize)

	// Fetch M5 candles: open_at - 2 bars before, close_at + 4h after — the
	// post-close window lets the chart show what price actually did after
	// the bot exited (would it have hit TP, kept running against, etc).
	candleStart := openAt.Add(-10 * time.Minute)
	candleEnd := closeAt.Add(4 * time.Hour)

	rows, err := h.db.Query(r.Context(), `
		SELECT bar_time, open, high, low, close
		FROM candles
		WHERE symbol_id = $1
		  AND period = 'M5'
		  AND bar_time >= $2 AND bar_time <= $3
		ORDER BY bar_time ASC
	`, symbolID, candleStart, candleEnd)
	if err != nil {
		slog.Error("candles query", "err", err)
	}

	candles := make([]Candle, 0)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var c Candle

			var bt time.Time
			if err := rows.Scan(&bt, &c.Open, &c.High, &c.Low, &c.Close); err != nil {
				continue
			}
			c.Time = bt.Unix()
			candles = append(candles, c)
		}
	}

	jsonOK(w, TradeDetail{Trade: t, Candles: candles})
}

// ── /live?symbol=XAUUSD&limit=20 ────────────────────────────────────────────

type LiveTick struct {
	Symbol     string  `json:"symbol"`
	Bid        float64 `json:"bid"`
	Ask        float64 `json:"ask"`
	ReceivedAt string  `json:"received_at"`
}

type LiveSignal struct {
	Symbol     string `json:"symbol"`
	Strategy   string `json:"strategy"`
	Signal     string `json:"signal"`
	Reason     string `json:"reason"`
	Confluence int    `json:"confluence"`
	CreatedAt  string `json:"created_at"`
}

type LiveResponse struct {
	Ticks   []LiveTick   `json:"ticks"`
	Signals []LiveSignal `json:"signals"`
}

func (h *handler) live(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	symbol := q.Get("symbol")

	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}

	symbolFilter := ""
	var args []any
	if symbol != "" {
		args = append(args, symbol)
		symbolFilter = "AND sym.symbol = $1"
	}
	args = append(args, limit)
	limitArg := fmt.Sprintf("$%d", len(args))

	ticks := make([]LiveTick, 0)
	rows, err := h.db.Query(r.Context(), `
		SELECT sym.symbol, pt.bid, pt.ask, pt.received_at
		FROM price_ticks pt
		JOIN symbols sym ON sym.id = pt.symbol_id
		WHERE 1=1 `+symbolFilter+`
		ORDER BY pt.received_at DESC
		LIMIT `+limitArg, args...)
	if err != nil {
		slog.Error("live ticks query", "err", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var t LiveTick
			var receivedAt time.Time
			if err := rows.Scan(&t.Symbol, &t.Bid, &t.Ask, &receivedAt); err != nil {
				continue
			}
			t.ReceivedAt = receivedAt.Format(time.RFC3339Nano)
			ticks = append(ticks, t)
		}
	}

	signals := make([]LiveSignal, 0)
	sigRows, err := h.db.Query(r.Context(), `
		SELECT sym.symbol, sig.strategy, sig.signal, COALESCE(sig.reason, ''), COALESCE(sig.confluence, 0), sig.created_at
		FROM signals sig
		JOIN symbols sym ON sym.id = sig.symbol_id
		WHERE 1=1 `+symbolFilter+`
		ORDER BY sig.created_at DESC
		LIMIT `+limitArg, args...)
	if err != nil {
		slog.Error("live signals query", "err", err)
	} else {
		defer sigRows.Close()
		for sigRows.Next() {
			var s LiveSignal
			var createdAt time.Time
			if err := sigRows.Scan(&s.Symbol, &s.Strategy, &s.Signal, &s.Reason, &s.Confluence, &createdAt); err != nil {
				continue
			}
			s.CreatedAt = createdAt.Format(time.RFC3339Nano)
			signals = append(signals, s)
		}
	}

	jsonOK(w, LiveResponse{Ticks: ticks, Signals: signals})
}

// ── Middleware ────────────────────────────────────────────────────────────────

func authMiddleware(key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-API-Key") != key {
				jsonErr(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func corsMiddleware(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "X-API-Key, Content-Type")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s not set", key)
	}
	return v
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func init() {
	// suppress unused import if strings is only used in CORS
	_ = strings.Contains
}
