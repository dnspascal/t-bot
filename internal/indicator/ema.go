package indicator

// EMA maintains incremental state for O(1) per-candle updates.
type EMA struct {
	period      int
	multiplier  float64
	value       float64
	count       int
	sum         float64
	initialized bool
}

func NewEMA(period int) *EMA {
	return &EMA{
		period:     period,
		multiplier: 2.0 / float64(period+1),
	}
}

// Add feeds one price into the EMA and returns the current value.
// Returns 0 until enough prices have been seen to seed the initial SMA.
func (e *EMA) Add(price float64) float64 {
	e.count++
	if !e.initialized {
		e.sum += price
		if e.count == e.period {
			e.value = e.sum / float64(e.period)
			e.initialized = true
		}
		return e.value
	}
	e.value = price*e.multiplier + e.value*(1-e.multiplier)
	return e.value
}

func (e *EMA) Value() float64 { return e.value }
func (e *EMA) IsReady() bool  { return e.initialized }
