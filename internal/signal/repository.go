package signal

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Insert(ctx context.Context, s Signal) (string, error) {
	const q = `
		INSERT INTO signals (symbol_id, provider, signal, reason, confluence, confidence, processing_us, checked_market_states, bar_time, strategy,
		                     skip_reason, intended_entry, intended_sl, intended_tp)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, ''), $12, $13, $14)
		RETURNING id`
	strat := s.Strategy
	if strat == "" {
		strat = "regime"
	}
	var id string
	err := r.db.QueryRow(ctx, q,
		s.SymbolID, s.Provider, s.Signal, s.Reason, s.Confluence, s.Confidence, s.ProcessingUS, s.CheckedMarketStates, s.BarTime, strat,
		s.SkipReason, s.IntendedEntry, s.IntendedSL, s.IntendedTP,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("signal.Insert: %w", err)
	}
	return id, nil
}
