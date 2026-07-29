package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const telegramAPI = "https://api.telegram.org/bot"

type TelegramChannel struct {
	token string
}

func NewTelegramChannel(token string) *TelegramChannel {
	return &TelegramChannel{token: token}
}

func (t *TelegramChannel) Name() string { return "telegram" }

func (t *TelegramChannel) Send(ctx context.Context, recipientID, replyMsgID string, eventType EventType, payload any) (string, error) {
	text := t.format(eventType, payload)
	if text == "" {
		return "", nil
	}
	msg := map[string]any{
		"chat_id":    recipientID,
		"text":       text,
		"parse_mode": "HTML",
	}
	if replyMsgID != "" {
		if id, err := strconv.ParseInt(replyMsgID, 10, 64); err == nil {
			msg["reply_to_message_id"] = id
		}
	}
	return t.post(ctx, "sendMessage", msg)
}

func (t *TelegramChannel) format(eventType EventType, payload any) string {
	switch eventType {
	case EventTradeOpened:
		p, ok := payload.(TradeOpenedPayload)
		if !ok {
			return ""
		}
		dir := "🟢 BUY"
		if p.Side == "SELL" {
			dir = "🔴 SELL"
		}
		return fmt.Sprintf(
			"%s <b>%s</b> opened\n📍 Price: %s\n🛡 SL: %s (%.1f pips)\n🎯 TP: %s (%.1f pips)\n🤖 Strategy: %s",
			dir, p.Symbol,
			formatPrice(p.Price),
			formatPrice(p.SLPrice), p.SLPips,
			formatPrice(p.TPPrice), p.TPPips,
			p.Strategy,
		)

	case EventTradeClosed:
		p, ok := payload.(TradeClosedPayload)
		if !ok {
			return ""
		}
		result := "✅ WIN"
		pnlSign := "+"
		if !p.IsWin {
			result = "❌ LOSS"
			pnlSign = ""
		}
		mins := int(p.Duration.Minutes())
		return fmt.Sprintf(
			"%s <b>%s %s</b> closed\n💰 P&amp;L: %s$%.2f\n⏱ Duration: %d min\n📍 Entry: %s → Close: %s",
			result, p.Side, p.Symbol,
			pnlSign, p.Realized,
			mins,
			formatPrice(p.EntryPrice), formatPrice(p.ClosePrice),
		)

	case EventDailySummary:
		p, ok := payload.(DailySummaryPayload)
		if !ok {
			return ""
		}
		pnlSign := "+"
		if p.Realized < 0 {
			pnlSign = ""
		}
		return fmt.Sprintf(
			"📊 <b>%s daily summary</b>\nTrades: %d (%dW / %dL)\nP&amp;L: %s$%.2f\nBalance: $%.2f",
			p.Symbol,
			p.TradeCount, p.WinCount, p.LossCount,
			pnlSign, p.Realized,
			p.Balance,
		)
	}
	return ""
}

// SendText sends a plain HTML message to a specific chat.
func (t *TelegramChannel) SendText(ctx context.Context, chatID, text string) error {
	_, err := t.post(ctx, "sendMessage", map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	})
	return err
}

// RegisterWebhook tells Telegram to POST updates to the given URL.
func (t *TelegramChannel) RegisterWebhook(ctx context.Context, webhookURL string) error {
	_, err := t.post(ctx, "setWebhook", map[string]any{
		"url":             webhookURL,
		"allowed_updates": []string{"message", "callback_query"},
	})
	return err
}

// Update is a Telegram update object.
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

type Message struct {
	Text string `json:"text"`
	Chat struct {
		ID        int64  `json:"id"`
		FirstName string `json:"first_name"`
		Username  string `json:"username"`
	} `json:"chat"`
	From struct {
		ID        int64  `json:"id"`
		FirstName string `json:"first_name"`
		Username  string `json:"username"`
	} `json:"from"`
}

// WebhookHandler returns an http.HandlerFunc that parses Telegram updates
// and calls handler for each one. secret must match the path suffix.
func (t *TelegramChannel) WebhookHandler(secret string, handler func(Update)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/"+secret) {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var update Update
		if err := json.Unmarshal(body, &update); err != nil {
			slog.Warn("telegram webhook: parse error", "err", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		handler(update)
		w.WriteHeader(http.StatusOK)
	}
}

func (t *TelegramChannel) post(ctx context.Context, method string, payload map[string]any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s%s/%s", telegramAPI, t.token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("telegram %s: %s", method, respBody)
	}
	var res struct {
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &res); err != nil || res.Result.MessageID == 0 {
		return "", nil
	}
	return fmt.Sprintf("%d", res.Result.MessageID), nil
}

func formatPrice(p float64) string {
	if p >= 100 {
		return fmt.Sprintf("%.2f", p)
	}
	return fmt.Sprintf("%.5f", p)
}
