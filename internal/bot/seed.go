package bot

import (
	"context"
	"log/slog"
	"sort"

	"github.com/denismgaya/t-bot/internal/config"
	strat "github.com/denismgaya/t-bot/internal/strategy"
)

// SeedStrategies loads past H1 bars into strategies that need history at startup (strat.H1Seeder).
func (b *Bot) SeedStrategies(ctx context.Context) {
	for _, s := range b.strategies {
		sd, ok := s.(strat.H1Seeder)
		if !ok {
			continue
		}
		candles, err := b.provider.FetchHistoricalCandles(ctx, b.symbol, config.PeriodH1, sd.SeedCount())
		if err != nil {
			slog.Warn("strategy seed failed — it waits for live days instead", "strategy", s.Name(), "err", err)
			continue
		}
		sort.Slice(candles, func(i, j int) bool { return candles[i].OpenTime < candles[j].OpenTime })
		bars := make([]strat.Bar, len(candles))
		for i, c := range candles {
			bars[i] = strat.Bar{OpenTime: c.OpenTime, High: c.High, Low: c.Low}
		}
		sd.SeedH1(bars)
		slog.Info("strategy seeded with H1 history", "strategy", s.Name(), "bars", len(bars))
	}
}
