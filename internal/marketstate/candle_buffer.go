package marketstate

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/denismgaya/t-bot/internal/indicator"
)

type CandleBuffer interface {
	// AddCandle adds a new candle to the buffer
	AddCandle(open, high, low, close float64, volume int64)

	// Closes returns all close prices in the buffer (chronological order)
	Closes() []float64

	// Volumes returns all volumes in the buffer (chronological order)
	Volumes() []int64

	// OHLC returns all OHLC data in the buffer (chronological order)
	OHLC() []indicator.OHLC

	// Count returns how many candles are in the buffer
	Count() int

	// IsWarmedUp returns true if buffer has enough candles for all indicators
	IsWarmedUp() bool
}

// MemoryCandleBuffer keeps candles in memory (fast, discarded on restart)
type MemoryCandleBuffer struct {
	maxSize int              // Keep this many candles (should be >= 21 for EMA slow)
	candles []indicator.OHLC // OHLC data in chronological order
	closes  []float64        // Just close prices
	volumes []int64          // Base asset volumes
}

func NewMemoryCandleBuffer(maxSize int) *MemoryCandleBuffer {
	if maxSize < 21 {
		maxSize = 21 // Minimum for EMA(21)
	}
	return &MemoryCandleBuffer{
		maxSize: maxSize,
		candles: make([]indicator.OHLC, 0, maxSize),
		closes:  make([]float64, 0, maxSize),
		volumes: make([]int64, 0, maxSize),
	}
}

// AddCandle adds a new candle and drops oldest if buffer is full
func (b *MemoryCandleBuffer) AddCandle(open, high, low, close float64, volume int64) {
	b.candles = append(b.candles, indicator.OHLC{Open: open, High: high, Low: low, Close: close})
	b.closes = append(b.closes, close)
	b.volumes = append(b.volumes, volume)

	if len(b.candles) > b.maxSize {
		b.candles = b.candles[1:]
		b.closes = b.closes[1:]
		b.volumes = b.volumes[1:]
	}
}

func (b *MemoryCandleBuffer) Closes() []float64 {
	result := make([]float64, len(b.closes))
	copy(result, b.closes)
	return result
}

func (b *MemoryCandleBuffer) Volumes() []int64 {
	result := make([]int64, len(b.volumes))
	copy(result, b.volumes)
	return result
}

func (b *MemoryCandleBuffer) OHLC() []indicator.OHLC {
	// Return copy to prevent external modification
	result := make([]indicator.OHLC, len(b.candles))
	copy(result, b.candles)
	return result
}

func (b *MemoryCandleBuffer) Count() int {
	return len(b.closes)
}

func (b *MemoryCandleBuffer) IsWarmedUp() bool {
	return len(b.closes) >= 21 // Minimum for EMA(21)
}

// DatabaseCandleBuffer loads candles from database (survives restarts, larger history)
type DatabaseCandleBuffer struct {
	db          *pgxpool.Pool
	symbolID    string
	provider    string
	period      string
	maxSize     int
	candles     []indicator.OHLC
	closes      []float64
	volumes     []int64
	lastBarTime int64
}

func NewDatabaseCandleBuffer(db *pgxpool.Pool, symbolID, provider, period string, maxSize int) *DatabaseCandleBuffer {
	if maxSize < 21 {
		maxSize = 21
	}
	return &DatabaseCandleBuffer{
		db:       db,
		symbolID: symbolID,
		provider: provider,
		period:   period,
		maxSize:  maxSize,
		candles:  make([]indicator.OHLC, 0, maxSize),
		closes:   make([]float64, 0, maxSize),
		volumes:  make([]int64, 0, maxSize),
	}
}

// LoadRecent loads the last N candles from the database
func (b *DatabaseCandleBuffer) LoadRecent(ctx context.Context, count int) error {
	rows, err := b.db.Query(ctx, `
		SELECT open, high, low, close, tick_volume, bar_time
		FROM candles
		WHERE symbol_id = $1 AND period = $2
		ORDER BY bar_time DESC
		LIMIT $3
	`, b.symbolID, b.period, count)
	if err != nil {
		return err
	}
	defer rows.Close()

	var tempCandles []indicator.OHLC
	var tempCloses []float64
	var tempVolumes []int64
	var barTimes []int64

	for rows.Next() {
		var open, high, low, close float64
		var volume, barTime int64
		if err := rows.Scan(&open, &high, &low, &close, &volume, &barTime); err != nil {
			return err
		}
		tempCandles = append(tempCandles, indicator.OHLC{Open: open, High: high, Low: low, Close: close})
		tempCloses = append(tempCloses, close)
		tempVolumes = append(tempVolumes, volume)
		barTimes = append(barTimes, barTime)
	}

	// Reverse to chronological order (earliest first)
	for i := len(tempCandles) - 1; i >= 0; i-- {
		b.candles = append(b.candles, tempCandles[i])
		b.closes = append(b.closes, tempCloses[i])
		b.volumes = append(b.volumes, tempVolumes[i])
	}

	if len(barTimes) > 0 {
		b.lastBarTime = barTimes[0]
	}

	return rows.Err()
}

// AddCandle adds a new candle from live stream and drops oldest if needed
func (b *DatabaseCandleBuffer) AddCandle(open, high, low, close float64, volume int64) {
	b.candles = append(b.candles, indicator.OHLC{Open: open, High: high, Low: low, Close: close})
	b.closes = append(b.closes, close)
	b.volumes = append(b.volumes, volume)

	if len(b.candles) > b.maxSize {
		b.candles = b.candles[1:]
		b.closes = b.closes[1:]
		b.volumes = b.volumes[1:]
	}
}

func (b *DatabaseCandleBuffer) Closes() []float64 {
	result := make([]float64, len(b.closes))
	copy(result, b.closes)
	return result
}

func (b *DatabaseCandleBuffer) Volumes() []int64 {
	result := make([]int64, len(b.volumes))
	copy(result, b.volumes)
	return result
}

func (b *DatabaseCandleBuffer) OHLC() []indicator.OHLC {
	result := make([]indicator.OHLC, len(b.candles))
	copy(result, b.candles)
	return result
}

func (b *DatabaseCandleBuffer) Count() int {
	return len(b.closes)
}

func (b *DatabaseCandleBuffer) IsWarmedUp() bool {
	return len(b.closes) >= 21
}
