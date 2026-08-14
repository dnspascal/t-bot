package indicator

import "math"

type ATR struct {
	period      int
	value       float64
	prevClose   float64
	sum         float64
	count       int
	initialized bool
}

func NewATR(period int) *ATR {
	return &ATR{period: period}
}

func (a *ATR) Add(high, low, close float64) float64 {
	a.count++

	if a.count == 1 {
		a.prevClose = close
		a.sum = high - low // Wilder: first candle TR = high - low (no prior close)
		return 0
	}

	tr := math.Max(high-low, math.Max(math.Abs(high-a.prevClose), math.Abs(low-a.prevClose)))
	a.prevClose = close

	if !a.initialized {
		a.sum += tr
		if a.count == a.period {
			a.value = a.sum / float64(a.period)
			a.initialized = true
		}
		return a.value
	}

	a.value = (a.value*float64(a.period-1) + tr) / float64(a.period)
	return a.value
}

func (a *ATR) Value() float64 { return a.value }
func (a *ATR) IsReady() bool  { return a.initialized }
