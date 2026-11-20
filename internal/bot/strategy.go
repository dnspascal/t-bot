package bot

// rsiMidline is used by watcher.go to evaluate invalidation signals on open positions.
// It is intentionally a universal constant, not strategy-specific.
const rsiMidline = 50.0

// slATRMult and tpATRMult are kept here for the forced-test-order override in bot.go.
const (
	slATRMult = 1.5
	tpATRMult = 2.5
)
