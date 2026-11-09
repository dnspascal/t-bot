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

const RecentBars = 6

type Calculator struct {
	ema9      *EMA
	ema21     *EMA
	ema50     *EMA
	ema200    *EMA
	rsi       *RSI
	atr       *ATR
	adx       *ADX
	emaGaps   []float64
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
