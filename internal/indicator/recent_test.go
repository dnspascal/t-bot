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
