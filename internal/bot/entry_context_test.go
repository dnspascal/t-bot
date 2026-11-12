package bot

import (
	"testing"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
)

func TestEntryContext(t *testing.T) {
	h1 := indicator.MarketState{PrevDayHigh: 4000, PrevDayLow: 3987, ATR: 16}
	// 12 Nov 12:10 SELL at 3932.14, yesterday's low ~3987: well beyond it.
	c := entryContext(config.SignalSell, 3932.14, h1)
	if got := c["beyond_prev_day_atr"].(float64); got < 3.4 || got > 3.5 {
		t.Errorf("sell beyond = %v, want (3987-3932.14)/16 ≈ 3.43", got)
	}
	if c := entryContext(config.SignalBuy, 3996, h1); c["beyond_prev_day_atr"].(float64) != -0.25 {
		t.Errorf("buy just under yesterday's high: got %v, want -0.25", c["beyond_prev_day_atr"])
	}
	if c := entryContext(config.SignalBuy, 3996, indicator.MarketState{ATR: 16}); c != nil {
		t.Errorf("no previous day yet: want nil, got %v", c)
	}
}
