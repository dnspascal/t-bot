package seed

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func SeedSymbols(ctx context.Context, db *pgxpool.Pool) error {
	symbols := []struct {
		symbol           string
		assetClass       string
		baseAsset        string
		quoteAsset       string
		exchange         string
		exchangeSymbolID string
		pipSize          float64
		priceDigits      int
		minVolume        int64
		maxVolume        int64
		maxDailyVolume   int64
		lotSize          int64
		tradingHours     string
		defaultSLPips    int
		defaultTPPips    int
	}{
		{
			symbol:           "EURUSD",
			assetClass:       "forex",
			baseAsset:        "EUR",
			quoteAsset:       "USD",
			exchange:         "ctrader",
			exchangeSymbolID: "1",
			pipSize:          0.0001,
			priceDigits:      5,
			minVolume:        100000,
			maxVolume:        10000000,
			maxDailyVolume:   50000000,
			lotSize:          1000000,
			tradingHours:     "07:00-22:00 UTC",
			defaultSLPips:    10,
			defaultTPPips:    20,
		},
		{
			symbol:           "XAUUSD",
			assetClass:       "commodity",
			baseAsset:        "XAU",
			quoteAsset:       "USD",
			exchange:         "ctrader",
			exchangeSymbolID: "41",
			pipSize:          0.10,
			priceDigits:      5,
			minVolume:        100,
			maxVolume:        50000,
			maxDailyVolume:   200000,
			lotSize:          100,
			tradingHours:     "01:00-23:59 UTC",
			defaultSLPips:    50,
			defaultTPPips:    100,
		},
		{
			symbol:           "XPDUSD",
			assetClass:       "commodity",
			baseAsset:        "XPD",
			quoteAsset:       "USD",
			exchange:         "ctrader",
			exchangeSymbolID: "73",
			pipSize:          0.10,
			priceDigits:      5,
			minVolume:        100,
			maxVolume:        50000,
			maxDailyVolume:   200000,
			lotSize:          100,
			tradingHours:     "01:00-23:59 UTC",
			defaultSLPips:    50,
			defaultTPPips:    100,
		},
		{
			symbol:           "XPTUSD",
			assetClass:       "commodity",
			baseAsset:        "XPT",
			quoteAsset:       "USD",
			exchange:         "ctrader",
			exchangeSymbolID: "74",
			pipSize:          0.10,
			priceDigits:      5,
			minVolume:        100,
			maxVolume:        50000,
			maxDailyVolume:   200000,
			lotSize:          100,
			tradingHours:     "01:00-23:59 UTC",
			defaultSLPips:    50,
			defaultTPPips:    100,
		},
		{
			symbol:           "BTCUSDT",
			assetClass:       "crypto",
			baseAsset:        "BTC",
			quoteAsset:       "USDT",
			exchange:         "binance",
			exchangeSymbolID: "BTCUSDT",
			pipSize:          0.01,
			minVolume:        1,
			maxVolume:        1000000,
			maxDailyVolume:   10000000,
			lotSize:          1,
			tradingHours:     "24/7",
			defaultSLPips:    10,
			defaultTPPips:    20,
		},
	}

	for _, s := range symbols {
		// Insert symbol (idempotent)
		_, err := db.Exec(ctx, `
			INSERT INTO symbols (symbol, asset_class, base_asset, quote_asset, exchange, exchange_symbol_id)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (symbol) DO UPDATE SET updated_at = NOW()
		`, s.symbol, s.assetClass, s.baseAsset, s.quoteAsset, s.exchange, s.exchangeSymbolID)
		if err != nil {
			return err
		}

		// Insert symbol config (idempotent)
		_, err = db.Exec(ctx, `
			INSERT INTO symbol_configs (symbol_id, pip_size, price_digits, min_volume, max_volume, max_daily_volume, lot_size, trading_hours, default_sl_pips, default_tp_pips)
			SELECT id, $2, $3, $4, $5, $6, $7, $8, $9, $10 FROM symbols WHERE symbol = $1
			ON CONFLICT (symbol_id) DO UPDATE SET pip_size = $2, price_digits = $3, updated_at = NOW()
		`, s.symbol, s.pipSize, s.priceDigits, s.minVolume, s.maxVolume, s.maxDailyVolume, s.lotSize, s.tradingHours, s.defaultSLPips, s.defaultTPPips)
		if err != nil {
			return err
		}
	}
	return nil
}
