package bot

import (
	"testing"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	strat "github.com/denismgaya/t-bot/internal/strategy"
)

func newShadowSell(open time.Time) *shadowTrade {
	return &shadowTrade{pos: trackedPosition{Side: config.SignalSell, OpenPrice: 100, SLPrice: 110, TPPrice: 80,
		OpenTime: open, MaxFavorable: 100, MaxAdverse: 100, StrategyName: "ema_pullback"}}
}

func TestShadowExitSLOnAskForSell(t *testing.T) {
	now := time.Now()
	tr := newShadowSell(now)
	if r, _, _ := shadowExit(tr, 109.8, 109.9, now, 0.1, 0.5); r != "" {
		t.Fatalf("ask below SL must not close, got %q", r)
	}
	r, exit, _ := shadowExit(tr, 109.9, 110.0, now, 0.1, 0.5)
	if r != strat.CloseReasonSLHit || exit != 110 {
		t.Fatalf("got %q at %v, want sl_hit at 110", r, exit)
	}
}

func TestShadowExitTP(t *testing.T) {
	now := time.Now()
	r, exit, _ := shadowExit(newShadowSell(now), 79.9, 80.0, now, 0.1, 0.5)
	if r != strat.CloseReasonTPHit || exit != 80 {
		t.Fatalf("got %q at %v, want tp_hit at 80", r, exit)
	}
}

func TestShadowExitProfitLockThenLockedSL(t *testing.T) {
	now := time.Now()
	tr := newShadowSell(now)
	if _, _, moved := shadowExit(tr, 84.9, 85.0, now, 0.1, 0.5); !moved || tr.pos.LockStage != 2 || tr.pos.SLPrice != 88 {
		t.Fatalf("75%% of TP reached: want stage 2 SL 88, got moved=%v stage %d SL %v", moved, tr.pos.LockStage, tr.pos.SLPrice)
	}
	r, exit, _ := shadowExit(tr, 87.9, 88.0, now, 0.1, 0.5)
	if r != strat.CloseReasonBreakevenSL || exit != 88 {
		t.Fatalf("got %q at %v, want breakeven_sl at 88", r, exit)
	}
}

func TestShadowExitTimeStops(t *testing.T) {
	open := time.Now().Add(-31 * time.Minute)
	if r, _, _ := shadowExit(newShadowSell(open), 100.4, 100.5, time.Now(), 0.1, 0.5); r != strat.CloseReasonTimeStopNeverProfitable30m {
		t.Fatalf("got %q, want never-profitable stop", r)
	}
	tr := newShadowSell(time.Now().Add(-61 * time.Minute))
	tr.pos.MaxFavorable = 98
	if r, _, _ := shadowExit(tr, 99.4, 99.5, time.Now(), 0.1, 0.5); r != strat.CloseReasonTimeStopStalled60m {
		t.Fatalf("got %q, want stall stop", r)
	}
}

func TestShadowCanOpenPerStrategyLimits(t *testing.T) {
	b := &Bot{pipSize: 0.1}
	b.shadowTrades = []*shadowTrade{newShadowSell(time.Now())}
	if b.shadowCanOpen("ema_pullback", config.SignalBuy, 99) {
		t.Fatal("opposite side within the same strategy must be blocked")
	}
	if b.shadowCanOpen("ema_pullback", config.SignalSell, 99.9) {
		t.Fatal("adding to a SELL not yet 2 pips in profit must be blocked")
	}
	if !b.shadowCanOpen("ema_pullback", config.SignalSell, 99.5) {
		t.Fatal("scale-in once the SELL is in profit should be allowed")
	}
	if !b.shadowCanOpen("sweep", config.SignalBuy, 99) {
		t.Fatal("other strategies' shadow trades must not block")
	}
	b.shadowTrades = append(b.shadowTrades, newShadowSell(time.Now()))
	if b.shadowCanOpen("ema_pullback", config.SignalSell, 99) {
		t.Fatal("max per strategy reached must block")
	}
}

func TestLiveStrategies(t *testing.T) {
	for _, n := range []string{"trend_follow", "session_open"} {
		if !isLiveStrategy(n) {
			t.Fatalf("%s should trade live", n)
		}
	}
	for _, n := range []string{"ema_pullback", "sweep", "session_momentum", "sr_bounce", "breakout"} {
		if isLiveStrategy(n) {
			t.Fatalf("%s should be shadow", n)
		}
	}
}

func TestMaxOpenPerStrategy(t *testing.T) {
	r := newPositionRegistry()
	r.Register(trackedPosition{ProviderPositionID: "1", Side: config.SignalBuy, Tier: config.TierNormal, OpenPrice: 4000, StrategyName: "trend_pullback"})
	if ok, why := r.CanOpen(config.TierNormal, config.SignalBuy, "trend_pullback", 4010, 0.1); ok || why != "max positions for this strategy reached" {
		t.Errorf("second trend_pullback: ok=%v (%s), want refused at 1 open", ok, why)
	}
	r.Register(trackedPosition{ProviderPositionID: "2", Side: config.SignalBuy, Tier: config.TierNormal, OpenPrice: 4000, StrategyName: "trend_follow"})
	if ok, why := r.CanOpen(config.TierNormal, config.SignalBuy, "trend_follow", 4010, 0.1); !ok {
		t.Errorf("second trend_follow: refused (%s), want allowed up to %d", why, maxPerStrategy)
	}
	if !isLiveStrategy("trend_pullback") || isLiveStrategy("trend_pullback_nogap") {
		t.Error("trend_pullback must be live and trend_pullback_nogap shadow")
	}
}
