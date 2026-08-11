package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/marketstate"
	"github.com/denismgaya/t-bot/internal/notify"
	"github.com/denismgaya/t-bot/internal/provider"
	"github.com/denismgaya/t-bot/internal/provider/binance"
	"github.com/denismgaya/t-bot/internal/provider/ctrader"
	"github.com/denismgaya/t-bot/internal/provider/ctrader/api"
)

func main() {

	setupLogging()

	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := setupGracefulShutdown()
	defer cancel()

	svc, err := initServices(ctx, cfg)
	if err != nil {
		log.Fatal("init services:", err)
	}
	defer svc.DB.Close()

	botStart := time.Now()

	svc.Repos.Events.Insert(ctx, "started", map[string]any{
		"enableCTrader": cfg.EnableCTrader,
		"enableBinance": cfg.EnableBinance,
	}, 0)

	// --- Telegram / notify setup ---
	dispatcher := notify.NewDispatcher(svc.DB.Pool)
	var tgChannel *notify.TelegramChannel
	if cfg.TelegramToken != "" {
		tgChannel = notify.NewTelegramChannel(cfg.TelegramToken)
		dispatcher.Register(tgChannel)

		if cfg.WebhookSecret != "" {
			webhookURL := fmt.Sprintf("https://signal.fx.denismgaya.com/webhook/%s", cfg.WebhookSecret)
			if err := tgChannel.RegisterWebhook(ctx, webhookURL); err != nil {
				slog.Warn("telegram: setWebhook failed", "err", err)
			} else {
				slog.Info("telegram: webhook registered", "url", webhookURL)
			}
		}
	}

	provMgr := provider.NewManager()
	var enabledProviders []string

	if cfg.EnableCTrader {
		enabledProviders = append(enabledProviders, "ctrader")

		priceDivisor, err := svc.Lookup.GetPriceDivisor(cfg.CTraderSymbol)
		if err != nil {
			log.Fatal("get price divisor:", err)
		}
		ctraderClient := api.NewClient(cfg.CTrader.Demo, cfg.CTrader.AccountID, cfg.CTrader.SymbolID, priceDivisor)
		if err := ctraderClient.Connect(); err != nil {
			log.Fatal("ctrader connect:", err)
		}
		ctraderProv := ctrader.New(cfg, ctraderClient, svc.DB.Pool, svc.Repos.Events, svc.Repos.Snapshots)
		if err := provMgr.Register("ctrader", ctraderProv); err != nil {
			log.Fatal("register ctrader:", err)
		}
	}

	if cfg.EnableBinance {
		enabledProviders = append(enabledProviders, "binance")

		binanceProv := binance.New(cfg, svc.DB.Pool, svc.Repos.Events, svc.Repos.Snapshots)
		if err := binanceProv.Connect(); err != nil {
			log.Fatal("binance connect:", err)
		}
		if err := provMgr.Register("binance", binanceProv); err != nil {
			log.Fatal("register binance:", err)
		}
	}

	if len(enabledProviders) == 0 {
		log.Fatal("no providers enabled")
	}

	authResults, err := provMgr.AuthAllProviders(ctx)
	if err != nil {
		log.Fatal("provider auth failed: ", err)
	}

	if err := provMgr.SetupAllProviders(ctx); err != nil {
		log.Fatal("provider setup failed: ", err)
	}


	// Collect live bot controllers for command handling
	var botsMu sync.Mutex
	var tradingInstances []botController

	registerTradingInstance := func(b botController) {
		botsMu.Lock()
		tradingInstances = append(tradingInstances, b)
		botsMu.Unlock()
	}

	var wg sync.WaitGroup

	if cfg.EnableCTrader {
		prov, _ := provMgr.GetProvider("ctrader")
		authResult := authResults["ctrader"]

		wg.Go(func() {
			startBotForProvider(ctx, cfg, svc, prov, cfg.CTraderSymbol, authResult, botStart, dispatcher, registerTradingInstance)
		})
	}

	if cfg.EnableBinance {
		prov, _ := provMgr.GetProvider("binance")
		authResult := authResults["binance"]

		wg.Go(func() {
			startBotForProvider(ctx, cfg, svc, prov, cfg.BinanceSymbol, authResult, botStart, dispatcher, registerTradingInstance)
		})
	}

	// Start webhook HTTP server (one per process, not per bot)
	if tgChannel != nil && cfg.WebhookSecret != "" {
		go func() {
			// Give bots a moment to register before we start accepting commands
			time.Sleep(2 * time.Second)
			handler := newTelegramCommandHandler(tgChannel, svc, func() []botController {
				botsMu.Lock()
				defer botsMu.Unlock()
				return tradingInstances
			})
			mux := http.NewServeMux()
			mux.HandleFunc("/webhook/", tgChannel.WebhookHandler(cfg.WebhookSecret, func(u notify.Update) {
				handler.Handle(ctx, u)
			}))
			addr := fmt.Sprintf(":%d", cfg.WebhookPort)
			slog.Info("telegram: webhook server listening", "addr", addr)
			if err := http.ListenAndServe(addr, mux); err != nil {
				slog.Error("telegram: webhook server error", "err", err)
			}
		}()
	}

	wg.Wait()

	if err := provMgr.CloseAllProviders(); err != nil {
		slog.Warn("provider close errors on shutdown", "err", err)
	}
	slog.Info("all bots stopped")
}

func startBotForProvider(
	ctx context.Context,
	cfg *config.Config,
	svc *Services,
	prov provider.Provider,
	symbol string,
	authResult *provider.AuthResult,
	botStart time.Time,
	dispatcher notify.Dispatcher,
	registerTradingInstance func(botController),
) {

	defer func() {
		if r := recover(); r != nil {
			slog.Error("bot panic recovered", "provider", prov.Name(), "symbol", symbol, "panic", r)
		}
	}()

	if authResult == nil {
		slog.Error("auth result missing — provider auth failed, bot will not start", "provider", prov.Name(), "symbol", symbol)
		return
	}

	symbolUUID, err := svc.Lookup.Get(symbol)
	if err != nil {
		slog.Error("get symbol uuid failed", "provider", prov.Name(), "symbol", symbol, "err", err)
		return
	}

	botResult := initializeBot(ctx, cfg, svc, prov, symbol, symbolUUID, authResult, dispatcher)
	registerTradingInstance(botResult.Bot)

	warmer := marketstate.NewWarmer(prov, botResult.ProcessorMgr, 100)
	if err := warmer.WarmupAllTimeframes(ctx, symbol); err != nil {
		slog.Error("warm-up failed — bot will not start", "provider", prov.Name(), "err", err)
		return
	}

	if err := prov.StartStreaming(); err != nil {
		slog.Error("start streaming failed", "provider", prov.Name(), "err", err)
		return
	}

	slog.Info("bot running",
		"provider", prov.Name(),
		"symbol", symbol,
		"balance", authResult.Balance,
		"riskPercent", cfg.RiskPercent,
		"maxDailyLossPct", fmt.Sprintf("%.0f%%", cfg.MaxDailyLossPct),
		"startupMs", elapsed(botStart),
	)

	backoff := 15 * time.Second
	const maxBackoff = 5 * time.Minute

	for attempt := 1; ; attempt++ {
		botResult.Bot.Run(ctx, botStart)

		if ctx.Err() != nil {
			return // graceful shutdown — don't retry
		}

		slog.Warn("provider disconnected — reconnecting",
			"provider", prov.Name(), "attempt", attempt, "backoff", backoff)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		if err := prov.Connect(); err != nil {
			slog.Error("reconnect: Connect failed", "provider", prov.Name(), "err", err)
		} else if _, err := prov.Auth(ctx); err != nil {
			slog.Error("reconnect: Auth failed", "provider", prov.Name(), "err", err)
		} else if err := prov.Setup(); err != nil {
			slog.Error("reconnect: Setup failed", "provider", prov.Name(), "err", err)
		} else if err := prov.StartStreaming(); err != nil {
			slog.Error("reconnect: StartStreaming failed", "provider", prov.Name(), "err", err)
		} else {
			slog.Info("reconnected", "provider", prov.Name(), "attempt", attempt)
			backoff = 15 * time.Second // reset on success
			botResult.Bot.Reset()
			continue
		}

		if backoff < maxBackoff {
			backoff *= 2
		}

		// One of the steps above failed — apply backoff and retry
		backoff = min(backoff*2, 5*time.Minute)
	}
}

func elapsed(t time.Time) int64 {
	return time.Since(t).Milliseconds()
}
