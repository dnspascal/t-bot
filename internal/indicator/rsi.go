package indicator

// RSI maintains incremental state for O(1) per-candle updates using Wilder's smoothing.
type RSI struct {
	period      int
	value       float64
	avgGain     float64
	avgLoss     float64
	prevClose   float64
	sumGain     float64
	sumLoss     float64
	count       int
	initialized bool
}

func NewRSI(period int) *RSI {
	return &RSI{period: period}
}

// Add feeds one close price and returns the current RSI.
// Returns 50 until enough prices have been seen to seed the initial averages.
func (r *RSI) Add(price float64) float64 {
	r.count++
	if r.count == 1 {
		r.prevClose = price
		return 50
	}

	change := price - r.prevClose
	r.prevClose = price

	var gain, loss float64
	if change >= 0 {
		gain = change
	} else {
		loss = -change
	}

	if !r.initialized {
		r.sumGain += gain
		r.sumLoss += loss
		if r.count == r.period+1 {
			r.avgGain = r.sumGain / float64(r.period)
			r.avgLoss = r.sumLoss / float64(r.period)
			r.value = r.rsi()
			r.initialized = true
		}
		return 50
	}

	r.avgGain = (r.avgGain*float64(r.period-1) + gain) / float64(r.period)
	r.avgLoss = (r.avgLoss*float64(r.period-1) + loss) / float64(r.period)
	r.value = r.rsi()
	return r.value
}

func (r *RSI) rsi() float64 {
	if r.avgLoss == 0 {
		return 100
	}
	return 100 - 100/(1+r.avgGain/r.avgLoss)
}

func (r *RSI) Value() float64 { return r.value }
func (r *RSI) IsReady() bool  { return r.initialized }
