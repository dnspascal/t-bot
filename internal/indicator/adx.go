package indicator

import "math"

type OHLC struct {
	Open  float64
	High  float64
	Low   float64
	Close float64
}

type ADX struct {
	period      int
	atr         float64
	plusDM      float64
	minusDM     float64
	value       float64
	prevHigh    float64
	prevLow     float64
	prevClose   float64
	sumTR       float64
	sumPlusDM   float64
	sumMinusDM  float64
	count       int
	initialized bool
}

func NewADX(period int) *ADX {
	return &ADX{period: period}
}

// Add feeds one OHLC candle and returns the current ADX.
// Returns 0 until the initial DX can be computed.
func (a *ADX) Add(high, low, close float64) float64 {
	a.count++
	if a.count == 1 {
		a.prevHigh = high
		a.prevLow = low
		a.prevClose = close
		return 0
	}

	tr := math.Max(math.Max(high-low, math.Abs(high-a.prevClose)), math.Abs(low-a.prevClose))

	upMove := high - a.prevHigh
	downMove := a.prevLow - low

	var plusDM, minusDM float64
	if upMove > downMove && upMove > 0 {
		plusDM = upMove
	}
	if downMove > upMove && downMove > 0 {
		minusDM = downMove
	}

	a.prevHigh = high
	a.prevLow = low
	a.prevClose = close

	if !a.initialized {
		a.sumTR += tr
		a.sumPlusDM += plusDM
		a.sumMinusDM += minusDM

		if a.count == a.period+1 {
			a.atr = a.sumTR / float64(a.period)
			a.plusDM = a.sumPlusDM / float64(a.period)
			a.minusDM = a.sumMinusDM / float64(a.period)

			pdi := (a.plusDM / a.atr) * 100
			mdi := (a.minusDM / a.atr) * 100
			if pdi+mdi == 0 {
				return 0
			}
			a.value = math.Abs(pdi-mdi) / (pdi + mdi) * 100
			a.initialized = true
		}
		return a.value
	}

	a.atr = (a.atr*float64(a.period-1) + tr) / float64(a.period)
	a.plusDM = (a.plusDM*float64(a.period-1) + plusDM) / float64(a.period)
	a.minusDM = (a.minusDM*float64(a.period-1) + minusDM) / float64(a.period)

	pdi := (a.plusDM / a.atr) * 100
	mdi := (a.minusDM / a.atr) * 100
	if pdi+mdi == 0 {
		return a.value
	}
	dx := math.Abs(pdi-mdi) / (pdi + mdi) * 100
	a.value = (a.value*float64(a.period-1) + dx) / float64(a.period)

	return a.value
}

func (a *ADX) Value() float64 { return a.value }
func (a *ADX) IsReady() bool  { return a.initialized }
