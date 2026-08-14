package candle

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Upsert(ctx context.Context, c Candle) error {
	const q = `
		INSERT INTO candles (symbol_id, period, open, high, low, close, tick_volume, bar_time, received_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (symbol_id, period, bar_time) DO UPDATE SET
			open        = EXCLUDED.open,
			high        = EXCLUDED.high,
			low         = EXCLUDED.low,
			close       = EXCLUDED.close,
			tick_volume = EXCLUDED.tick_volume,
			received_at = EXCLUDED.received_at`
	_, err := r.db.Exec(ctx, q,
		c.SymbolID, c.Period,
		c.Open, c.High, c.Low, c.Close,
		c.TickVolume, c.BarTime, c.ReceivedAt,
	)
	if err != nil {
		return fmt.Errorf("candle.Upsert: %w", err)
	}
	return nil
}

func (r *Repository) Recent(ctx context.Context, symbolID, period string, n int) ([]Candle, error) {
	const q = `
		SELECT id, symbol_id, period, open, high, low, close, tick_volume, bar_time, received_at
		FROM (
			SELECT * FROM candles
			WHERE symbol_id = $1 AND period = $2
			ORDER BY bar_time DESC
			LIMIT $3
		) sub
		ORDER BY bar_time ASC`
	rows, err := r.db.Query(ctx, q, symbolID, period, n)
	if err != nil {
		return nil, fmt.Errorf("candle.Recent: %w", err)
	}
	defer rows.Close()

	var candles []Candle
	for rows.Next() {
		var c Candle
		if err := rows.Scan(
			&c.ID, &c.SymbolID, &c.Period,
			&c.Open, &c.High, &c.Low, &c.Close,
			&c.TickVolume, &c.BarTime, &c.ReceivedAt,
		); err != nil {
			return nil, fmt.Errorf("candle.Recent scan: %w", err)
		}
		candles = append(candles, c)
	}
	return candles, nil
}

// Since returns candles for symbolID/period from (and including) since onward, ascending
// by bar_time. Used to reconstruct price history a still-open position lived through —
// e.g. after a bot restart, to recompute its true peak/trough rather than losing it.
func (r *Repository) Since(ctx context.Context, symbolID, period string, since time.Time) ([]Candle, error) {
	const q = `
		SELECT id, symbol_id, period, open, high, low, close, tick_volume, bar_time, received_at
		FROM candles
		WHERE symbol_id = $1 AND period = $2 AND bar_time >= $3
		ORDER BY bar_time ASC`
	rows, err := r.db.Query(ctx, q, symbolID, period, since)
	if err != nil {
		return nil, fmt.Errorf("candle.Since: %w", err)
	}
	defer rows.Close()

	var candles []Candle
	for rows.Next() {
		var c Candle
		if err := rows.Scan(
			&c.ID, &c.SymbolID, &c.Period,
			&c.Open, &c.High, &c.Low, &c.Close,
			&c.TickVolume, &c.BarTime, &c.ReceivedAt,
		); err != nil {
			return nil, fmt.Errorf("candle.Since scan: %w", err)
		}
		candles = append(candles, c)
	}
	return candles, nil
}
