package api

import (
	"math"
	"testing"
)

// 5 Nov 04:00 H1: open 4088.5, high 4135.1, low 4084.3, real close ~4124.6.
func encodeBar(withClose bool) []byte {
	var b []byte
	b = appendUint32(b, 4, 9)
	b = appendInt64(b, 5, 408430000)
	b = appendInt64(b, 6, 420000)
	if withClose {
		b = appendInt64(b, 7, 4030000)
	}
	b = appendInt64(b, 8, 5080000)
	b = appendInt64(b, 9, 29_400_000)
	return b
}

func liveSpotPayload(bar []byte) []byte {
	var p []byte
	p = appendInt64(p, 2, 41)
	return appendBytes(p, 6, bar)
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestLiveTrendbarCloseFromBid(t *testing.T) {
	bars := decodeLiveTrendbarEvents(liveSpotPayload(encodeBar(false)), 4124.6)
	if len(bars) != 1 {
		t.Fatalf("got %d bars", len(bars))
	}
	b := bars[0]
	if !near(b.Close, 4124.6) {
		t.Errorf("close = %v, want bid 4124.6 (low is %v)", b.Close, b.Low)
	}
	if !near(b.Open, 4088.5) || !near(b.High, 4135.1) || !near(b.Low, 4084.3) {
		t.Errorf("OHLC wrong: %+v", b)
	}
}

func TestLiveTrendbarBidClampedToRange(t *testing.T) {
	b := decodeLiveTrendbarEvents(liveSpotPayload(encodeBar(false)), 4200)[0]
	if !near(b.Close, 4135.1) {
		t.Errorf("close = %v, want clamped to high 4135.1", b.Close)
	}
}

func TestLiveTrendbarKeepsSentClose(t *testing.T) {
	b := decodeLiveTrendbarEvents(liveSpotPayload(encodeBar(true)), 4100)[0]
	if !near(b.Close, 4124.6) {
		t.Errorf("close = %v, want sent close 4124.6", b.Close)
	}
}

func TestHistoricalTrendbarClose(t *testing.T) {
	var res []byte
	res = appendBytes(res, 5, encodeBar(true))
	b := decodeGetTrendbarsRes(res)[0]
	if !near(b.Close, 4124.6) {
		t.Errorf("close = %v, want 4124.6", b.Close)
	}
}
