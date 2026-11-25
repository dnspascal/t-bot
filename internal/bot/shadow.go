package bot

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/indicator"
	strat "github.com/denismgaya/t-bot/internal/strategy"
)

const SkipReasonShadow = "shadow"

// Only these strategies trade real money; the rest open virtual trades in shadow_trades through the same exits.
var liveStrategies = map[string]bool{
	"trend_follow":         true,
	"session_open":         true,
	"trend_pullback":       true,
	"trend_pullback_nogap": true,
	"reopen_hour":          true,
	"month_start":          true,
	"trend_follow_bigday":  true,
}

func isLiveStrategy(name string) bool { return liveStrategies[name] }

type shadowTrade struct {
	id       string
	signalID string
	pos      trackedPosition
	sl0      float64
	maxHold  time.Duration // set for strat.MaxHolder strategies; replaces the 30/60-minute time stops
}

// shadowExit applies one price to a shadow trade (BUY exits on bid, SELL on ask) and reports a live exit once one fires.
func shadowExit(t *shadowTrade, bid, ask float64, now time.Time, pipSize, minLock float64) (reason string, exit float64, slMoved bool) {
	p := &t.pos
	buy := p.Side == config.SignalBuy
	px := ask
	if buy {
		px = bid
	}
	if buy && px > p.MaxFavorable || !buy && px < p.MaxFavorable {
		p.MaxFavorable = px
	}
	if buy && px < p.MaxAdverse || !buy && px > p.MaxAdverse {
		p.MaxAdverse = px
	}

	if buy && px <= p.SLPrice || !buy && px >= p.SLPrice {
		if p.LockStage > 0 {
			return strat.CloseReasonBreakevenSL, p.SLPrice, false
		}
		return strat.CloseReasonSLHit, p.SLPrice, false
	}
	if buy && px >= p.TPPrice || !buy && px <= p.TPPrice {
		return strat.CloseReasonTPHit, p.TPPrice, false
	}

	if stage, newSL := profitLockTarget(*p, minLock); stage > p.LockStage {
		if buy && newSL > p.SLPrice || !buy && newSL < p.SLPrice {
			p.SLPrice = newSL
			slMoved = true
		}
		p.LockStage = stage
	}

	age := now.Sub(p.OpenTime)
	if t.maxHold > 0 {
		if age >= t.maxHold {
			return strat.CloseReasonTimeStopMaxHold, px, slMoved
		}
		return "", 0, slMoved
	}
	if age >= neverProfitableTimeout &&
		(buy && p.MaxFavorable <= p.OpenPrice+pipSize || !buy && p.MaxFavorable >= p.OpenPrice-pipSize) {
		return strat.CloseReasonTimeStopNeverProfitable30m, px, slMoved
	}
	if age >= stallTimeout {
		tpDist, peak := p.TPPrice-p.OpenPrice, p.MaxFavorable-p.OpenPrice
		if !buy {
			tpDist, peak = -tpDist, -peak
		}
		if tpDist > 0 && peak/tpDist*100 < stallMaxFavPct {
			return strat.CloseReasonTimeStopStalled60m, px, slMoved
		}
	}
	return "", 0, slMoved
}

// shadowCanOpen applies the per-strategy limits of PositionRegistry.CanOpen to the strategy's own shadow trades.
func (b *Bot) shadowCanOpen(strategyName, side string, price float64) bool {
	n := 0
	for _, t := range b.shadowTrades {
		if t.pos.StrategyName != strategyName {
			continue
		}
		if t.pos.Side != side {
			return false
		}
		minDist := minScaleInPips * b.pipSize
		if side == config.SignalBuy && price < t.pos.OpenPrice+minDist || side == config.SignalSell && price > t.pos.OpenPrice-minDist {
			return false
		}
		n++
	}
	return n < maxOpenFor(strategyName)
}

func (b *Bot) openShadowTrade(ctx context.Context, result strat.EntryResult, signalID string) {
	entry := b.currentPrice.Bid
	if result.Signal == config.SignalBuy {
		entry = b.currentPrice.Ask
	}
	if entry <= 0 || !b.shadowCanOpen(result.StrategyName, result.Signal, entry) {
		return
	}
	now := time.Now()
	t := &shadowTrade{signalID: signalID, sl0: result.SLPrice, pos: trackedPosition{
		Side: result.Signal, Tier: result.Tier, Volume: b.lotUnit, OpenPrice: entry, SLPrice: result.SLPrice,
		TPPrice: result.TPPrice, ATR: result.ATR, OpenTime: now, MaxFavorable: entry, MaxAdverse: entry,
		StrategyName: result.StrategyName,
	}, maxHold: b.maxHoldFor(result.StrategyName)}
	var sigID any
	if signalID != "" {
		sigID = signalID
	}
	if err := b.db.QueryRow(ctx,
		`INSERT INTO shadow_trades (signal_id, symbol_id, strategy, side, volume, entry_price, sl, tp, current_sl, max_favorable, max_adverse, opened_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $7, $6, $6, $9) RETURNING id`,
		sigID, b.symbolUUID, result.StrategyName, result.Signal, b.lotUnit, entry, result.SLPrice, result.TPPrice, now,
	).Scan(&t.id); err != nil {
		slog.Error("shadow trade insert failed", "strategy", result.StrategyName, "err", err)
		return
	}
	b.shadowTrades = append(b.shadowTrades, t)
	slog.Info("shadow trade opened — no order sent",
		"strategy", result.StrategyName, "side", result.Signal, "entry", entry, "sl", result.SLPrice, "tp", result.TPPrice)
}

// checkShadowTrades runs the tick-level exits (SL, TP, profit lock, time stops) on every open shadow trade.
func (b *Bot) checkShadowTrades(ctx context.Context, price float64) {
	bid, ask := b.currentPrice.Bid, b.currentPrice.Ask
	if bid <= 0 || ask <= 0 {
		bid, ask = price, price
	}
	minLock := b.breakEvenBufferPips() * b.pipSize
	now := time.Now()
	for _, t := range append([]*shadowTrade(nil), b.shadowTrades...) {
		reason, exit, moved := shadowExit(t, bid, ask, now, b.pipSize, minLock)
		if reason != "" {
			b.closeShadowTrade(ctx, t, reason, exit, now)
			continue
		}
		if moved {
			if _, err := b.db.Exec(ctx, `UPDATE shadow_trades SET current_sl = $2, lock_stage = $3 WHERE id = $1`,
				t.id, t.pos.SLPrice, t.pos.LockStage); err != nil {
				slog.Warn("shadow trade lock update failed", "id", t.id, "err", err)
			}
		}
	}
}

// watchShadowTrades runs the M5-close exits: EOD close and, for strategies that use it, invalidation.
func (b *Bot) watchShadowTrades(ctx context.Context, ms indicator.MarketState) {
	now := time.Now()
	for _, t := range append([]*shadowTrade(nil), b.shadowTrades...) {
		exit := b.currentPrice.Ask
		if t.pos.Side == config.SignalBuy {
			exit = b.currentPrice.Bid
		}
		if exit <= 0 {
			exit = ms.Close
		}
		if b.isEODWindow() {
			b.closeShadowTrade(ctx, t, strat.CloseReasonEODClose, exit, now)
			continue
		}
		if !b.usesTrendWatcher(t.pos.StrategyName) {
			continue
		}
		if n, signals := countInvalidationSignals(ms, t.pos); n >= signalsToClose {
			b.closeShadowTrade(ctx, t, strat.InvalidationPrefix+strings.Join(signals, ","), exit, now)
		}
	}
}

func (b *Bot) closeShadowTrade(ctx context.Context, t *shadowTrade, reason string, exit float64, at time.Time) {
	diff := exit - t.pos.OpenPrice
	if t.pos.Side == config.SignalSell {
		diff = -diff
	}
	usd := b.unrealizedUSD(diff, t.pos.Volume)
	if _, err := b.db.Exec(ctx,
		`UPDATE shadow_trades SET closed_at = $2, close_price = $3, close_reason = $4, pnl_per_oz = $5, pnl_usd = $6,
		 current_sl = $7, lock_stage = $8, max_favorable = $9, max_adverse = $10 WHERE id = $1`,
		t.id, at, exit, reason, diff, usd, t.pos.SLPrice, t.pos.LockStage, t.pos.MaxFavorable, t.pos.MaxAdverse,
	); err != nil {
		slog.Error("shadow trade close update failed", "id", t.id, "err", err)
	}
	for i, x := range b.shadowTrades {
		if x == t {
			b.shadowTrades = append(b.shadowTrades[:i], b.shadowTrades[i+1:]...)
			break
		}
	}
	b.notifyStrategyClosed(t.pos.StrategyName, t.pos.Side, reason, at)
	slog.Info("shadow trade closed", "strategy", t.pos.StrategyName, "side", t.pos.Side, "reason", reason,
		"entry", t.pos.OpenPrice, "exit", exit, "pnlPerOz", diff, "pnlUSD", usd)
}

// loadShadowTrades restores open shadow trades after a restart; peaks are rebuilt from M5 closes since the open.
func (b *Bot) loadShadowTrades(ctx context.Context) {
	rows, err := b.db.Query(ctx,
		`SELECT id, COALESCE(signal_id::text, ''), strategy, side, volume, entry_price::float8, sl::float8, tp::float8,
		        current_sl::float8, lock_stage, opened_at
		 FROM shadow_trades WHERE symbol_id = $1 AND closed_at IS NULL`, b.symbolUUID)
	if err != nil {
		slog.Error("shadow trades load failed", "err", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		t := &shadowTrade{}
		p := &t.pos
		if err := rows.Scan(&t.id, &t.signalID, &p.StrategyName, &p.Side, &p.Volume, &p.OpenPrice, &t.sl0, &p.TPPrice,
			&p.SLPrice, &p.LockStage, &p.OpenTime); err != nil {
			slog.Error("shadow trade scan failed", "err", err)
			continue
		}
		t.maxHold = b.maxHoldFor(p.StrategyName)
		p.MaxFavorable, p.MaxAdverse = p.OpenPrice, p.OpenPrice
		if fav, adv := b.recoverPeaks(ctx, b.symbolUUID, p.Side, p.OpenPrice, p.OpenTime); fav != 0 {
			p.MaxFavorable, p.MaxAdverse = fav, adv
		}
		b.shadowTrades = append(b.shadowTrades, t)
	}
	if len(b.shadowTrades) > 0 {
		slog.Info("shadow trades restored", "count", len(b.shadowTrades))
	}
}
