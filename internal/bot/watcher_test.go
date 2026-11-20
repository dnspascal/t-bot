package bot

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
)

func TestPeakDrawbackDisabledKeepsPositionAndMarksShadowOnce(t *testing.T) {
	b := &Bot{registry: newPositionRegistry(), pipSize: 0.1, pendingCloseReasons: map[string]pendingClose{}}
	b.registry.Register(trackedPosition{ProviderPositionID: "1", Side: config.SignalSell, OpenPrice: 3746.54,
		SLPrice: 3762.98, TPPrice: 3720.79, OpenTime: time.Now(), LockStage: len(profitLockSteps)})
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

func TestProfitLockTarget(t *testing.T) {
	sell := trackedPosition{Side: config.SignalSell, OpenPrice: 3700, TPPrice: 3680}
	buy := trackedPosition{Side: config.SignalBuy, OpenPrice: 3700, TPPrice: 3720}
	cases := []struct {
		pos       trackedPosition
		peak      float64
		wantStage int
		wantSL    float64
	}{
		{sell, 3691, 0, 0},
		{sell, 3690, 1, 3698},
		{sell, 3685, 2, 3688},
		{sell, 3682, 3, 3686},
		{buy, 3710, 1, 3702},
		{buy, 3715, 2, 3712},
		{buy, 3718.5, 3, 3714},
	}
	for _, c := range cases {
		c.pos.MaxFavorable = c.peak
		stage, sl := profitLockTarget(c.pos, 0.5)
		if stage != c.wantStage || math.Abs(sl-c.wantSL) > 1e-9 {
			t.Errorf("%s peak %.1f: got stage %d SL %.2f, want %d %.2f", c.pos.Side, c.peak, stage, sl, c.wantStage, c.wantSL)
		}
	}
	small := trackedPosition{Side: config.SignalBuy, OpenPrice: 3700, TPPrice: 3702, MaxFavorable: 3701}
	if _, sl := profitLockTarget(small, 0.5); math.Abs(sl-3700.5) > 1e-9 {
		t.Errorf("first step should lock at least the $0.50 buffer, got SL %.2f", sl)
	}
}

func TestCheckBreakEvenSkipsAmendWhenSLAlreadyLocked(t *testing.T) {
	b := &Bot{registry: newPositionRegistry(), pipSize: 0.1}
	b.registry.Register(trackedPosition{ProviderPositionID: "2", Side: config.SignalSell, OpenPrice: 3700,
		TPPrice: 3680, SLPrice: 3688, MaxFavorable: 3685})
	pos, _ := b.registry.Get("2")
	b.checkBreakEven(context.Background(), pos) // nil provider: an amend here would panic
	if pos, _ = b.registry.Get("2"); pos.LockStage != 2 {
		t.Fatalf("got LockStage %d, want 2", pos.LockStage)
	}
}

func TestReversalSignalsIgnoreRegime(t *testing.T) {
	pos := trackedPosition{Side: config.SignalSell, OpenPrice: 3700, TPPrice: 3680}
	flat := indicator.MarketState{Regime: config.Ranging, RSI: 45, EMAFast: 1, EMASlow: 2, MomentumDirection: config.MomentumStable}
	if n, sigs := countReversalSignals(flat, pos); n != 0 {
		t.Fatalf("ranging regime alone should not vote: got %d %v", n, sigs)
	}
	turned := indicator.MarketState{Regime: config.TrendingUp, RSI: 55, EMAFast: 2, EMASlow: 1, MomentumDirection: config.MomentumStable}
	if n, sigs := countReversalSignals(turned, pos); n != signalsToClose {
		t.Fatalf("EMA cross + RSI against a SELL should reach the close threshold: got %d %v", n, sigs)
	}
}
