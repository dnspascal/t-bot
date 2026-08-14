package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"

	"github.com/denismgaya/t-bot/internal/bot"
	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/marketstate"
	"github.com/denismgaya/t-bot/internal/ml"
	"github.com/denismgaya/t-bot/internal/notify"
	"github.com/denismgaya/t-bot/internal/provider"
	"github.com/denismgaya/t-bot/internal/risk"
	"github.com/denismgaya/t-bot/internal/strategy"
	"github.com/denismgaya/t-bot/internal/strategy/breakout"
	combined "github.com/denismgaya/t-bot/internal/strategy/combined"
	ddearlybreakout "github.com/denismgaya/t-bot/internal/strategy/dd_early_breakout"
	ddoversoldbounce "github.com/denismgaya/t-bot/internal/strategy/dd_oversold_bounce"
	ddrangingbreakout "github.com/denismgaya/t-bot/internal/strategy/dd_ranging_breakout"
	emapullback "github.com/denismgaya/t-bot/internal/strategy/ema_pullback"
	"github.com/denismgaya/t-bot/internal/strategy/regime"
	rsireversal "github.com/denismgaya/t-bot/internal/strategy/rsi_reversal"
	sessionmomentum "github.com/denismgaya/t-bot/internal/strategy/session_momentum"
	sessionopen "github.com/denismgaya/t-bot/internal/strategy/session_open"
	srbounce "github.com/denismgaya/t-bot/internal/strategy/sr_bounce"
	trendfollow "github.com/denismgaya/t-bot/internal/strategy/trend_follow"
)

type BotInitResult struct {
	Bot          *bot.Bot
	RiskManager  *risk.Manager
	ProcessorMgr *marketstate.ProcessorManager
	Balance      float64
}

func initializeBot(ctx context.Context, cfg *config.Config, svc *Services, prov provider.Provider, symbol string, symbolUUID string, authResult *provider.AuthResult, dispatcher notify.Dispatcher) *BotInitResult {
	balance := authResult.Balance

	todayLoss, err := svc.Repos.PnLs.Today(ctx, symbolUUID)
	if err != nil {
		log.Fatal("load daily pnl:", err)
	}
	riskMgr := risk.New(cfg.RiskPercent, cfg.MaxDailyLossPct, cfg.EnableRiskMgmt)
	switch prov.Name() {
	case "ctrader":
		// cTrader API: 100,000 units = 1 micro lot (0.01 lots). Matches V1.
		// pipValue=0.10: 1 pip on 0.01 lots EURUSD = $0.10/pip.
		// min=100,000 (1 micro lot), max=5,000,000 (50 micro lots = 0.5 lots).
		riskMgr.SetVolumeConfig(100_000, 100_000, 5_000_000, 0.10)
	case "binance":
		// unitsPerMicroLot=100_000 satoshis (0.001 BTC per micro lot).
		// pipValue=1e-7: 0.0001 price move × 0.001 BTC = $0.0000001/pip/micro-lot.
		// minVolume=100_000 satoshis (0.001 BTC) — the Binance futures LOT_SIZE minimum;
		// anything smaller floors to qty=0 in the %.3f format used by PlaceMarketOrder.
		riskMgr.SetVolumeConfig(100_000, 100_000, 5_000_000, 1e-7)
	}
	riskMgr.RestorePnL(todayLoss)

	processorMgr := marketstate.NewProcessorManager(symbolUUID, prov.Name(), svc.Repos.MarketState)
	if cfg.DevMode {
		processorMgr.SkipWarmup("H4", "D1")
		slog.Info("dev mode: H4 and D1 warmup not required", "provider", prov.Name())
	}

	for _, period := range config.TradingPeriods {
		buf := marketstate.NewMemoryCandleBuffer(100)
		proc := marketstate.NewProcessor(symbolUUID, prov.Name(), period, buf, svc.Repos.MarketState)
		processorMgr.AddProcessor(period, proc)
	}
	pipSize, err := svc.Lookup.GetPipSize(symbol)
	if err != nil {
		log.Fatal("get pip size:", err)
	}

	var lotUnit int64 = 100_000
	if prov.Name() == "ctrader" && symbol == "XAUUSD" {
		lotUnit = 100
	}

	strategies, err := buildStrategies(cfg.Strategy, symbol, cfg.MLModelDir, cfg.MLOnnxLib)
	if err != nil {
		log.Fatal("build strategy:", err)
	}
	names := make([]string, len(strategies))
	for i, s := range strategies {
		names[i] = s.Name()
	}
	slog.Info("strategies loaded", "strategies", names, "provider", prov.Name())

	tradingBot := bot.New(cfg, prov, strategies, symbol, symbolUUID, authResult.AccountID, pipSize, lotUnit, svc.DB.Pool, riskMgr, balance, authResult.Leverage, svc.Lookup, svc.Repos.Ticks, svc.Repos.Candles, svc.Repos.Signals, svc.Repos.Orders, svc.Repos.Fills, svc.Repos.Positions, svc.Repos.PnLs, svc.Repos.Events, processorMgr, dispatcher)

	return &BotInitResult{
		Bot:          tradingBot,
		RiskManager:  riskMgr,
		ProcessorMgr: processorMgr,
		Balance:      balance,
	}
}

func buildStrategies(name, symbol, mlModelDir, mlOnnxLib string) ([]strategy.Strategy, error) {
	newSRBounce := func() *srbounce.SRBounce {
		return buildSRBounce(symbol, mlModelDir, mlOnnxLib)
	}
	switch name {
	case "", "all":
		return []strategy.Strategy{
			newSRBounce(),
			breakout.New(),
			sessionopen.New(),
			trendfollow.New(),
			rsireversal.New(),
			sessionmomentum.New(),
			emapullback.New(),
			ddoversoldbounce.New(),
			ddrangingbreakout.New(),
			ddearlybreakout.New(),
		}, nil
	case "regime":
		return []strategy.Strategy{regime.New()}, nil
	case "sr_bounce":
		return []strategy.Strategy{newSRBounce()}, nil
	case "trend_follow":
		return []strategy.Strategy{trendfollow.New()}, nil
	case "breakout":
		return []strategy.Strategy{breakout.New()}, nil
	case "session_open":
		return []strategy.Strategy{sessionopen.New()}, nil
	case "rsi_reversal":
		return []strategy.Strategy{rsireversal.New()}, nil
	case "session_momentum":
		return []strategy.Strategy{sessionmomentum.New()}, nil
	case "ema_pullback":
		return []strategy.Strategy{emapullback.New()}, nil
	case "dd_oversold_bounce":
		return []strategy.Strategy{ddoversoldbounce.New()}, nil
	case "dd_ranging_breakout":
		return []strategy.Strategy{ddrangingbreakout.New()}, nil
	case "dd_early_breakout":
		return []strategy.Strategy{ddearlybreakout.New()}, nil
	case "dd_all":
		return []strategy.Strategy{ddoversoldbounce.New(), ddrangingbreakout.New(), ddearlybreakout.New()}, nil
	case "combined":
		return []strategy.Strategy{combined.New(trendfollow.New(), newSRBounce())}, nil
	default:
		return nil, fmt.Errorf("unknown strategy %q — valid options: all, regime, sr_bounce, trend_follow, breakout, session_open, rsi_reversal, session_momentum, ema_pullback, combined, dd_oversold_bounce, dd_ranging_breakout, dd_early_breakout, dd_all", name)
	}
}

func buildSRBounce(symbol, mlModelDir, mlOnnxLib string) *srbounce.SRBounce {
	var modelFile string
	var threshold float32
	var symbolID float32

	switch symbol {
	case "XAUUSD":
		modelFile = mlModelDir + "/xauusd_model.onnx"
		threshold = 0.75
		symbolID = 1
	default: // EURUSD
		modelFile = mlModelDir + "/eurusd_model.onnx"
		threshold = 0.65
		symbolID = 0
	}

	predictor, err := ml.NewPredictor(modelFile, mlOnnxLib)
	if err != nil {
		slog.Warn("ml predictor not loaded — running without ML filter", "symbol", symbol, "err", err)
		return srbounce.New(nil, 0, symbolID)
	}
	slog.Info("ml predictor loaded", "symbol", symbol, "model", modelFile, "threshold", threshold)
	return srbounce.New(predictor, threshold, symbolID)
}
