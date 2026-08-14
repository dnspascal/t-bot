package main

import (
	"context"

	"github.com/denismgaya/t-bot/internal/candle"
	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/database"
	"github.com/denismgaya/t-bot/internal/event"
	"github.com/denismgaya/t-bot/internal/fill"
	"github.com/denismgaya/t-bot/internal/marketstate"
	"github.com/denismgaya/t-bot/internal/notify"
	"github.com/denismgaya/t-bot/internal/order"
	"github.com/denismgaya/t-bot/internal/pnl"
	"github.com/denismgaya/t-bot/internal/position"
	"github.com/denismgaya/t-bot/internal/signal"
	"github.com/denismgaya/t-bot/internal/snapshot"
	"github.com/denismgaya/t-bot/internal/symbol"
	"github.com/denismgaya/t-bot/internal/tick"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Database struct {
	*pgxpool.Pool
}

type Repositories struct {
	Ticks       *tick.Repository
	Candles     *candle.Repository
	Signals     *signal.Repository
	Orders      *order.Repository
	Fills       *fill.Repository
	Positions   *position.Repository
	PnLs        *pnl.Repository
	Events      *event.Repository
	Snapshots   *snapshot.Repository
	MarketState marketstate.Repository
	Subscribers *notify.SubscriberRepo
}

type Services struct {
	DB     *Database
	Repos  *Repositories
	Lookup *symbol.SymbolLookup
}

func initServices(ctx context.Context, cfg *config.Config) (*Services, error) {
	pool, err := database.New(ctx, cfg.DatabaseURL, 10, 2)
	if err != nil {
		return nil, err
	}

	// Load symbols for all enabled providers
	var symbols []string
	if cfg.EnableCTrader {
		symbols = append(symbols, cfg.CTraderSymbol)
	}
	if cfg.EnableBinance {
		symbols = append(symbols, cfg.BinanceSymbol)
	}

	lookup, err := symbol.LoadLookup(ctx, pool, symbols)
	if err != nil {
		pool.Close()
		return nil, err
	}

	repos := &Repositories{
		Ticks:       tick.New(pool),
		Candles:     candle.New(pool),
		Signals:     signal.New(pool),
		Orders:      order.New(pool),
		Fills:       fill.New(pool),
		Positions:   position.New(pool),
		PnLs:        pnl.New(pool),
		Events:      event.New(pool),
		Snapshots:   snapshot.New(pool),
		MarketState: marketstate.NewPostgresRepository(pool),
		Subscribers: notify.NewSubscriberRepo(pool),
	}

	return &Services{
		DB:     &Database{pool},
		Repos:  repos,
		Lookup: lookup,
	}, nil
}
