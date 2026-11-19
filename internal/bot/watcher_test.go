package bot

import (
	"context"
	"testing"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
)

func TestPeakDrawbackDisabledKeepsPositionAndMarksShadowOnce(t *testing.T) {
	b := &Bot{registry: newPositionRegistry(), pipSize: 0.1, pendingCloseReasons: map[string]pendingClose{}}
	b.registry.Register(trackedPosition{ProviderPositionID: "1", Side: config.SignalSell, OpenPrice: 3746.54,
		SLPrice: 3762.98, TPPrice: 3720.79, OpenTime: time.Now(), BreakEvenGaveUp: true})
	ctx := context.Background()

	b.checkPeakDrawback(ctx, 3728.35) // +18.19 peak, past 50% of TP distance
	b.checkPeakDrawback(ctx, 3742.20) // back to +4.34, below 25% of the peak

	pos, ok := b.registry.Get("1")
	if !ok {
		t.Fatal("position was removed")
	}
	if _, pending := b.pendingCloseReasons["1"]; pending {
		t.Fatal("peak drawback sent a close while disabled")
	}
	if !pos.PeakDrawbackShadow {
		t.Fatal("shadow flag not set")
	}
	if b.registry.MarkPeakDrawbackShadow("1") {
		t.Fatal("shadow should be recorded only once")
	}
}

func TestReversalSignalsIgnorePeakDrawbackWhileDisabled(t *testing.T) {
	pos := trackedPosition{Side: config.SignalSell, OpenPrice: 3746.54, TPPrice: 3720.79, MaxFavorable: 3728.35}
	ms := indicator.MarketState{Close: 3742.20, Regime: config.Breakout, RSI: 50, EMAFast: 1, EMASlow: 1, MomentumDirection: config.MomentumStable}
	if n, sigs := countReversalSignals(ms, pos); n != 0 {
		t.Fatalf("got %d signals %v, want 0", n, sigs)
	}
}
