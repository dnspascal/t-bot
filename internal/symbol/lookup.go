package symbol

import (
	"context"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5/pgxpool"
)

type SymbolLookup struct {
	uuids         map[string]string
	pipSizes      map[string]float64
	priceDivisors map[string]float64
}

func (s *SymbolLookup) Get(name string) (string, error) {
	uuid, ok := s.uuids[name]
	if !ok {
		return "", fmt.Errorf("symbol not found: %s", name)
	}
	return uuid, nil
}

func (s *SymbolLookup) GetPipSize(name string) (float64, error) {
	ps, ok := s.pipSizes[name]
	if !ok {
		return 0, fmt.Errorf("pip size not found for symbol: %s", name)
	}
	return ps, nil
}

func (s *SymbolLookup) GetPriceDivisor(name string) (float64, error) {
	d, ok := s.priceDivisors[name]
	if !ok {
		return 0, fmt.Errorf("price divisor not found for symbol: %s", name)
	}
	return d, nil
}

func LoadLookup(ctx context.Context, pool *pgxpool.Pool, symbolNames []string) (*SymbolLookup, error) {
	rows, err := pool.Query(ctx,
		`SELECT s.symbol, s.id, sc.pip_size, sc.price_digits
		 FROM symbols s
		 JOIN symbol_configs sc ON sc.symbol_id = s.id
		 WHERE s.symbol = ANY($1) AND sc.deleted_at IS NULL`,
		symbolNames,
	)
	if err != nil {
		return nil, fmt.Errorf("query symbols: %w", err)
	}
	defer rows.Close()

	uuids := make(map[string]string)
	pipSizes := make(map[string]float64)
	priceDivisors := make(map[string]float64)
	for rows.Next() {
		var sym, uuid string
		var pipSize float64
		var priceDigits int
		if err := rows.Scan(&sym, &uuid, &pipSize, &priceDigits); err != nil {
			return nil, fmt.Errorf("scan symbol: %w", err)
		}
		uuids[sym] = uuid
		pipSizes[sym] = pipSize
		priceDivisors[sym] = math.Pow(10, float64(priceDigits))
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate symbols: %w", err)
	}

	if len(uuids) == 0 {
		return nil, fmt.Errorf("no symbols found in database")
	}

	return &SymbolLookup{uuids: uuids, pipSizes: pipSizes, priceDivisors: priceDivisors}, nil
}
