package indicator

type MarketState struct {
	ID           string
	SymbolID     string
	Provider     string
	Period       string
	BarTime      int64
	ProcessingUS int64 // microseconds from WebSocket receive to market state stored

	// OHLCV
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume int64

	// Indicators
	EMAFast float64 // EMA(9)
	EMASlow float64 // EMA(21)
	EMA50   float64 // EMA(50)
	EMA200  float64 // EMA(200)
	RSI     float64 // RSI(14)
	ADX     float64 // ADX(14)
	ATR     float64 // ATR(14)

	// Structure (support/resistance)
	SupportLevel    float64
	ResistanceLevel float64
	TrendHigh       float64
	TrendLow        float64
	BreakoutLevel   float64

	// Over the last RecentBars bars, this one included
	RecentHigh      float64
	RecentLow       float64
	RecentEMAGapMax float64 // max of EMAFast-EMASlow
	RecentEMAGapMin float64 // min of EMAFast-EMASlow

	// High/low of the RangeBars bars before this one (this bar excluded); 0 until enough history
	RangeHigh float64
	RangeLow  float64

	// Previous UTC day's high/low as seen by this timeframe; 0 until a full day has passed
	PrevDayHigh float64
	PrevDayLow  float64

	// Regime classification
	Regime            string // trending_up, trending_down, ranging, breakout
	VolatilityTrend   string // expanding, contracting, stable
	MomentumDirection string // rising, falling, stable

	// Volume context
	VolumeMA int64

	// Session open range (populated during the London 08:00 and NY 09:00 local-time open windows)
	SessionHigh float64
	SessionLow  float64

	// Status
	IsWarmedUp bool // True when EMA(21) is seeded
}

const (
	RecentBars = 6
	RangeBars  = 15
)

type Calculator struct {
	ema9      *EMA
	ema21     *EMA
	ema50     *EMA
	ema200    *EMA
	rsi       *RSI
	atr       *ATR
	adx       *ADX
	emaGaps   []float64
	day       dayRange
	lastState MarketState
}

func NewCalculator() *Calculator {
	return &Calculator{
		ema9:   NewEMA(9),
		ema21:  NewEMA(21),
		ema50:  NewEMA(50),
		ema200: NewEMA(200),
		rsi:    NewRSI(14),
		atr:    NewATR(14),
		adx:    NewADX(14),
	}
}

func (c *Calculator) Calculate(
	symbolID, provider, period string,
	barTime int64,
	open, high, low, close float64,
	volume int64,
	historicalCloses []float64,
	historicalVolumes []int64,
	historicalOHLC []OHLC,
) MarketState {
	c.ema9.Add(close)
	c.ema21.Add(close)
	c.ema50.Add(close)
	c.ema200.Add(close)
	c.rsi.Add(close)
	c.atr.Add(high, low, close)
	c.adx.Add(high, low, close)

	allCloses := append(historicalCloses, close)
	allVolumes := append(historicalVolumes, volume)
	allOHLC := append(historicalOHLC, OHLC{Open: open, High: high, Low: low, Close: close})

	ms := MarketState{
		SymbolID: symbolID,
		Provider: provider,
		Period:   period,
		BarTime:  barTime,
		Open:     open,
		High:     high,
		Low:      low,
		Close:    close,
		Volume:   volume,
		EMAFast:  c.ema9.Value(),
		EMASlow:  c.ema21.Value(),
		EMA50:    c.ema50.Value(),
		EMA200:   c.ema200.Value(),
		RSI:      c.rsi.Value(),
		ADX:      c.adx.Value(),
		ATR:      c.atr.Value(),
	}

	ms.IsWarmedUp = c.ema21.IsReady()
	ms.Regime = CalculateRegime(ms.EMAFast, ms.EMASlow, ms.ADX, high, low, allOHLC)
	ms.VolatilityTrend = CalculateVolatilityTrend(ms.ATR, c.lastState.ATR)
	ms.MomentumDirection = CalculateMomentumDirection(ms.RSI, allCloses)
	ms.SupportLevel, ms.ResistanceLevel, ms.TrendHigh, ms.TrendLow = CalculateSupportResistance(allOHLC)
	ms.BreakoutLevel = CalculateBreakoutLevel(high, low, allOHLC)
	ms.VolumeMA = CalculateVolumeMA(allVolumes, 20)
	c.fillRecent(&ms, allOHLC)
	fillRange(&ms, allOHLC)
	ms.PrevDayHigh, ms.PrevDayLow = c.day.update(barTime, high, low)

	c.lastState = ms
	return ms
}

func (c *Calculator) LastState() MarketState {
	return c.lastState
}

func (c *Calculator) fillRecent(ms *MarketState, allOHLC []OHLC) {
	recent := allOHLC[max(0, len(allOHLC)-RecentBars):]
	ms.RecentHigh, ms.RecentLow = recent[0].High, recent[0].Low
	for _, b := range recent[1:] {
		ms.RecentHigh = max(ms.RecentHigh, b.High)
		ms.RecentLow = min(ms.RecentLow, b.Low)
	}

	if !ms.IsWarmedUp {
		return
	}
	c.emaGaps = append(c.emaGaps, ms.EMAFast-ms.EMASlow)
	if len(c.emaGaps) > RecentBars {
		c.emaGaps = c.emaGaps[1:]
	}
	ms.RecentEMAGapMax, ms.RecentEMAGapMin = c.emaGaps[0], c.emaGaps[0]
	for _, g := range c.emaGaps[1:] {
		ms.RecentEMAGapMax = max(ms.RecentEMAGapMax, g)
		ms.RecentEMAGapMin = min(ms.RecentEMAGapMin, g)
	}
}

func fillRange(ms *MarketState, allOHLC []OHLC) {
	prior := allOHLC[:len(allOHLC)-1]
	if len(prior) < RangeBars {
		return
	}
	prior = prior[len(prior)-RangeBars:]
	ms.RangeHigh, ms.RangeLow = prior[0].High, prior[0].Low
	for _, b := range prior[1:] {
		ms.RangeHigh = max(ms.RangeHigh, b.High)
		ms.RangeLow = min(ms.RangeLow, b.Low)
	}
}

type dayRange struct {
	day                      int64
	high, low                float64
	prevHigh, prevLow        float64
	sawDayStart, havePrevDay bool
}

// update folds one bar into the current UTC day and returns the previous complete day's high/low.
func (d *dayRange) update(barTime int64, high, low float64) (float64, float64) {
	day := barTime / 86400
	switch {
	case d.day == 0:
		d.day, d.high, d.low = day, high, low
	case day != d.day:
		if d.sawDayStart {
			d.prevHigh, d.prevLow, d.havePrevDay = d.high, d.low, true
		}
		d.day, d.high, d.low, d.sawDayStart = day, high, low, true
	default:
		d.high, d.low = max(d.high, high), min(d.low, low)
	}
	if !d.havePrevDay {
		return 0, 0
	}
	return d.prevHigh, d.prevLow
}
