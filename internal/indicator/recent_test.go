package indicator

import "testing"

func TestRecentWindow(t *testing.T) {
	c := NewCalculator()
	var hist []OHLC
	var ms MarketState
	for i := 0; i < 40; i++ {
		p := 100 + float64(i)
		hi, lo := p+1, p-1
		if i == 30 {
			hi = 500 // outside the last 6 bars by the end
		}
		if i == 36 {
			lo = 50 // inside the last 6 bars
		}
		ms = c.Calculate("s", "p", "H1", int64(i), p, hi, lo, p, 1, nil, nil, hist)
		hist = append(hist, OHLC{Open: p, High: hi, Low: lo, Close: p})
	}
	if ms.RecentHigh != 140 {
		t.Errorf("RecentHigh = %v, want 140 (bar 30 spike is older than %d bars)", ms.RecentHigh, RecentBars)
	}
	if ms.RecentLow != 50 {
		t.Errorf("RecentLow = %v, want 50", ms.RecentLow)
	}
	gap := ms.EMAFast - ms.EMASlow
	if ms.RecentEMAGapMax < gap || ms.RecentEMAGapMin > gap || len(c.emaGaps) != RecentBars {
		t.Errorf("gap range [%v,%v] must include current gap %v over %d bars", ms.RecentEMAGapMin, ms.RecentEMAGapMax, gap, len(c.emaGaps))
	}
}

func TestRangeExcludesCurrentBar(t *testing.T) {
	c := NewCalculator()
	var hist []OHLC
	var ms MarketState
	for i := 0; i < 30; i++ {
		hi, lo := 101.0, 99.0
		if i == 10 {
			hi = 150 // older than the 15-bar window at the end
		}
		if i == 20 {
			lo = 90 // inside the window
		}
		if i == 29 {
			hi, lo = 200, 50 // the current bar must not count
		}
		ms = c.Calculate("s", "p", "M15", int64(i), 100, hi, lo, 100, 1, nil, nil, hist)
		hist = append(hist, OHLC{Open: 100, High: hi, Low: lo, Close: 100})
	}
	if ms.RangeHigh != 101 || ms.RangeLow != 90 {
		t.Errorf("range = %v/%v, want 101/90", ms.RangeHigh, ms.RangeLow)
	}

	c2 := NewCalculator()
	if ms := c2.Calculate("s", "p", "M15", 0, 100, 101, 99, 100, 1, nil, nil, nil); ms.RangeHigh != 0 {
		t.Errorf("range should be 0 without 15 bars of history, got %v", ms.RangeHigh)
	}
}

func TestPrevDayRange(t *testing.T) {
	c := NewCalculator()
	var hist []OHLC
	feed := func(ts int64, hi, lo float64) MarketState {
		ms := c.Calculate("s", "p", "H1", ts, 100, hi, lo, 100, 1, nil, nil, hist)
		hist = append(hist, OHLC{Open: 100, High: hi, Low: lo, Close: 100})
		return ms
	}
	const day = 86400
	start := int64(20000 * day)
	// Partial first day (bot started at 18:00): must not count as a previous day.
	for h := int64(18); h < 24; h++ {
		if ms := feed(start+h*3600, 200, 50); ms.PrevDayHigh != 0 {
			t.Fatalf("partial day reported as previous day")
		}
	}
	// Full day: high 120, low 80.
	for h := int64(0); h < 24; h++ {
		hi, lo := 105.0, 95.0
		if h == 10 {
			hi = 120
		}
		if h == 15 {
			lo = 80
		}
		if ms := feed(start+day+h*3600, hi, lo); ms.PrevDayHigh != 0 {
			t.Fatalf("hour %d: previous day must still be unknown (first day was partial)", h)
		}
	}
	ms := feed(start+2*day, 101, 99)
	if ms.PrevDayHigh != 120 || ms.PrevDayLow != 80 {
		t.Errorf("prev day = %v/%v, want 120/80", ms.PrevDayHigh, ms.PrevDayLow)
	}
	ms = feed(start+2*day+3600, 300, 10) // today's extremes must not leak into "previous day"
	if ms.PrevDayHigh != 120 || ms.PrevDayLow != 80 {
		t.Errorf("prev day changed intraday: %v/%v", ms.PrevDayHigh, ms.PrevDayLow)
	}
}
