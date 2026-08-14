package ctrader

import (
	"log/slog"

	"github.com/denismgaya/t-bot/internal/config"
)

func (c *CTrader) Setup() error {
	if c.client == nil {
		return nil
	}

	if err := c.client.SubscribeSpots(); err != nil {
		return err
	}

	for _, period := range config.TradingPeriods {
		p := stringToPeriod(period)
		if err := c.client.SubscribeLiveTrendbar(p); err != nil {
			return err
		}
	}

	slog.Info("subscribed to ctrader streams", "periods", config.TradingPeriods)
	return nil
}
