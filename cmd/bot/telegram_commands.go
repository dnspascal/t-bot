package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/denismgaya/t-bot/internal/notify"
)

type botController interface {
	Symbol() string
	Pause()
	Resume()
	IsPaused() bool
	StatusText(ctx context.Context) string
	TodayText(ctx context.Context) string
	TriggerTestClose() string
	TriggerTestAmend() string
}

type telegramCommandHandler struct {
	tg                  *notify.TelegramChannel
	db                  *Services
	getTradingInstances func() []botController
}

func newTelegramCommandHandler(tg *notify.TelegramChannel, svc *Services, getTradingInstances func() []botController) *telegramCommandHandler {
	return &telegramCommandHandler{tg: tg, db: svc, getTradingInstances: getTradingInstances}
}

func (h *telegramCommandHandler) Handle(ctx context.Context, update notify.Update) {
	if update.Message == nil || update.Message.Text == "" {
		return
	}

	chatID := fmt.Sprintf("%d", update.Message.Chat.ID)
	text := strings.TrimSpace(update.Message.Text)
	firstName := update.Message.From.FirstName

	parts := strings.Fields(text)
	if len(parts) == 0 {
		return
	}
	cmd := strings.ToLower(parts[0])
	if i := strings.Index(cmd, "@"); i != -1 {
		cmd = cmd[:i]
	}

	switch cmd {
	case "/start":
		h.handleStart(ctx, chatID, firstName)
	case "/status":
		h.handleStatus(ctx, chatID)
	case "/today":
		h.handleToday(ctx, chatID)
	case "/pause":
		h.handlePause(ctx, chatID)
	case "/resume":
		h.handleResume(ctx, chatID)
	case "/balance":
		h.handleBalance(ctx, chatID)
	case "/testclose":
		h.handleTestClose(ctx, chatID)
	case "/testamend":
		h.handleTestAmend(ctx, chatID)
	}
}

func (h *telegramCommandHandler) handleStart(ctx context.Context, chatID, firstName string) {
	if err := h.db.Repos.Subscribers.Upsert(ctx, chatID, firstName); err != nil {
		slog.Warn("telegram: subscriber upsert failed", "chatID", chatID, "err", err)
		h.tg.SendText(ctx, chatID, "Something went wrong. Try again later.")
		return
	}
	h.tg.SendText(ctx, chatID, fmt.Sprintf(
		"Welcome %s! You will now receive trade signals.\n\nCommands:\n/status — bot status\n/today — today's summary\n/balance — account balance\n/pause — stop new trades\n/resume — resume trading",
		firstName,
	))
}

func (h *telegramCommandHandler) handleStatus(ctx context.Context, chatID string) {
	var parts []string
	for _, b := range h.getTradingInstances() {
		parts = append(parts, b.StatusText(ctx))
	}
	h.tg.SendText(ctx, chatID, strings.Join(parts, "\n\n"))
}

func (h *telegramCommandHandler) handleToday(ctx context.Context, chatID string) {
	var parts []string
	for _, b := range h.getTradingInstances() {
		parts = append(parts, b.TodayText(ctx))
	}
	h.tg.SendText(ctx, chatID, strings.Join(parts, "\n\n"))
}

func (h *telegramCommandHandler) handlePause(ctx context.Context, chatID string) {
	for _, b := range h.getTradingInstances() {
		b.Pause()
	}
	h.tg.SendText(ctx, chatID, "All bots paused. No new trades will be opened.")
}

func (h *telegramCommandHandler) handleResume(ctx context.Context, chatID string) {
	for _, b := range h.getTradingInstances() {
		b.Resume()
	}
	h.tg.SendText(ctx, chatID, "All bots resumed.")
}

func (h *telegramCommandHandler) handleBalance(ctx context.Context, chatID string) {
	var parts []string
	for _, b := range h.getTradingInstances() {
		parts = append(parts, b.StatusText(ctx))
	}
	h.tg.SendText(ctx, chatID, strings.Join(parts, "\n\n"))
}

func (h *telegramCommandHandler) handleTestClose(ctx context.Context, chatID string) {
	var results []string
	for _, b := range h.getTradingInstances() {
		results = append(results, b.TriggerTestClose())
	}
	h.tg.SendText(ctx, chatID, strings.Join(results, "\n"))
}

func (h *telegramCommandHandler) handleTestAmend(ctx context.Context, chatID string) {
	var results []string
	for _, b := range h.getTradingInstances() {
		results = append(results, b.TriggerTestAmend())
	}
	h.tg.SendText(ctx, chatID, strings.Join(results, "\n"))
}
