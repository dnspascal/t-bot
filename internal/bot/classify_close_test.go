package bot

import (
	"testing"

	"github.com/denismgaya/t-bot/internal/config"
	strat "github.com/denismgaya/t-bot/internal/strategy"
)

func TestClassifyBrokerClose(t *testing.T) {
	b := &Bot{pipSize: 0.01}
	buy := trackedPosition{Side: config.SignalBuy, OpenPrice: 4000, SLPrice: 4000.05, TPPrice: 4020, BreakEvenActive: true}
	sell := trackedPosition{Side: config.SignalSell, OpenPrice: 4357.74, SLPrice: 4357.69, TPPrice: 4328.63, BreakEvenActive: true}
	cases := []struct {
		name string
		pos  trackedPosition
		px   float64
		want string
	}{
		{"buy TP hit after breakeven moved", buy, 4020.01, strat.CloseReasonTPHit},
		{"sell TP hit after breakeven moved (2026 case)", sell, 4328.62, strat.CloseReasonTPHit},
		{"buy stopped at breakeven", buy, 4000.05, strat.CloseReasonBreakevenSL},
		{"sell stopped at breakeven", sell, 4357.69, strat.CloseReasonBreakevenSL},
		{"buy original SL", trackedPosition{Side: config.SignalBuy, OpenPrice: 4000, SLPrice: 3990, TPPrice: 4020}, 3989.98, strat.CloseReasonSLHit},
	}
	for _, c := range cases {
		if got := b.classifyBrokerClose(c.pos, c.px); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}
