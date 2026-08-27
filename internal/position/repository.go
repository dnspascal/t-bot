package position

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

func (r *Repository) Upsert(ctx context.Context, p Position) (string, error) {
	const q = `
		INSERT INTO positions
			(our_order_id, provider, provider_position_id, provider_acct_id, symbol_id, side, volume,
			 tier, open_price, current_sl, current_tp, swap, commission, used_margin,
			 status, trailing_stop_loss, guaranteed_stop_loss, label, comment, decision_params,
			 open_timestamp, close_timestamp, raw_payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
		ON CONFLICT (provider, provider_position_id) DO UPDATE SET
			open_price           = EXCLUDED.open_price,
			current_sl           = EXCLUDED.current_sl,
			current_tp           = EXCLUDED.current_tp,
			tier                 = EXCLUDED.tier,
			swap                 = EXCLUDED.swap,
			commission           = EXCLUDED.commission,
			used_margin          = EXCLUDED.used_margin,
			status               = EXCLUDED.status,
			trailing_stop_loss   = EXCLUDED.trailing_stop_loss,
			close_timestamp      = EXCLUDED.close_timestamp,
			raw_payload          = EXCLUDED.raw_payload,
			updated_at           = NOW()
		RETURNING id`
	// decision_params is intentionally absent from the ON CONFLICT SET list above — it's
	// a snapshot of what was live at open time and must stay immutable across later syncs.
	var id string
	err := r.db.QueryRow(ctx, q,
		p.OurOrderID, p.Provider, p.ProviderPositionID, p.ProviderAcctID, p.SymbolID, p.Side, p.Volume,
		p.Tier, p.OpenPrice, p.CurrentSL, p.CurrentTP, p.Swap, p.Commission, p.UsedMargin,
		p.Status, p.TrailingStopLoss, p.GuaranteedStopLoss, p.Label, p.Comment, p.DecisionParams,
		p.OpenTimestamp, p.CloseTimestamp, p.RawPayload,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("position.Upsert: %w", err)
	}
	return id, nil
}

func (r *Repository) IDByProviderPositionID(ctx context.Context, provider, providerPositionID string) (string, error) {
	var id string
	err := r.db.QueryRow(ctx,
		`SELECT id FROM positions WHERE provider = $1 AND provider_position_id = $2`,
		provider, providerPositionID,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("position.IDByProviderPositionID: %w", err)
	}
	return id, nil
}

// Close marks a position as closed and writes the high-water marks tracked during its life.
func (r *Repository) Close(ctx context.Context, provider, providerPositionID string, closeTime time.Time, maxFavorable, maxAdverse *float64) error {
	const q = `
		UPDATE positions SET
			status          = 'closed',
			close_timestamp = $3,
			max_favorable   = $4,
			max_adverse     = $5,
			updated_at      = NOW()
		WHERE provider = $1 AND provider_position_id = $2`
	_, err := r.db.Exec(ctx, q, provider, providerPositionID, closeTime, maxFavorable, maxAdverse)
	if err != nil {
		return fmt.Errorf("position.Close: %w", err)
	}
	return nil
}

func (r *Repository) OpenByProvider(ctx context.Context, provider, symbolID string) ([]Position, error) {
	// Strategy is joined in here (not part of the positions table itself) so
	// that startup reconcile can restore trackedPosition.StrategyName — without
	// it, a position that survives a restart loses its strategy attribution
	// and silently falls back to the shared reversal watcher, even for
	// strategies that explicitly opt out of it (UsesTrendWatcher() == false).
	const q = `
		SELECT p.id, p.provider, p.provider_position_id, p.provider_acct_id, p.symbol_id, p.side, p.volume,
		       p.tier, p.open_price, p.current_sl, p.current_tp, p.swap, p.commission, p.used_margin,
		       p.status, p.trailing_stop_loss, p.guaranteed_stop_loss, p.label, p.comment,
		       p.open_timestamp, p.close_timestamp, p.created_at, p.updated_at, COALESCE(sig.strategy, '')
		FROM positions p
		LEFT JOIN orders o ON o.id = p.our_order_id
		LEFT JOIN signals sig ON sig.id = o.signal_id
		WHERE p.status = 'open' AND p.provider = $1 AND p.symbol_id = $2`
	rows, err := r.db.Query(ctx, q, provider, symbolID)
	if err != nil {
		return nil, fmt.Errorf("position.OpenByProvider: %w", err)
	}
	defer rows.Close()

	var positions []Position
	for rows.Next() {
		var p Position
		if err := rows.Scan(
			&p.ID, &p.Provider, &p.ProviderPositionID, &p.ProviderAcctID, &p.SymbolID,
			&p.Side, &p.Volume, &p.Tier, &p.OpenPrice, &p.CurrentSL, &p.CurrentTP,
			&p.Swap, &p.Commission, &p.UsedMargin,
			&p.Status, &p.TrailingStopLoss, &p.GuaranteedStopLoss,
			&p.Label, &p.Comment, &p.OpenTimestamp, &p.CloseTimestamp,
			&p.CreatedAt, &p.UpdatedAt, &p.Strategy,
		); err != nil {
			return nil, fmt.Errorf("position.OpenByProvider scan: %w", err)
		}
		positions = append(positions, p)
	}
	return positions, nil
}

func (r *Repository) Open(ctx context.Context) ([]Position, error) {
	const q = `
		SELECT id, provider, provider_position_id, provider_acct_id, symbol_id, side, volume,
		       tier, open_price, current_sl, current_tp, swap, commission, used_margin,
		       status, trailing_stop_loss, guaranteed_stop_loss, label, comment,
		       open_timestamp, close_timestamp, created_at, updated_at
		FROM positions
		WHERE status = 'open'`
	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("position.Open: %w", err)
	}
	defer rows.Close()

	var positions []Position
	for rows.Next() {
		var p Position
		if err := rows.Scan(
			&p.ID, &p.Provider, &p.ProviderPositionID, &p.ProviderAcctID, &p.SymbolID,
			&p.Side, &p.Volume, &p.Tier, &p.OpenPrice, &p.CurrentSL, &p.CurrentTP,
			&p.Swap, &p.Commission, &p.UsedMargin,
			&p.Status, &p.TrailingStopLoss, &p.GuaranteedStopLoss,
			&p.Label, &p.Comment, &p.OpenTimestamp, &p.CloseTimestamp,
			&p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("position.Open scan: %w", err)
		}
		positions = append(positions, p)
	}
	return positions, nil
}
