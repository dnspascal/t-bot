package config

var TradingPeriods = []string{"M1", "M5", "M15", "M30", "H1", "H4", "D1"}

var PeriodToBinanceInterval = map[string]string{
	"M1":  "1m",
	"M5":  "5m",
	"M15": "15m",
	"M30": "30m",
	"H1":  "1h",
	"H4":  "4h",
	"D1":  "1d",
}

func BinanceIntervals() []string {
	intervals := make([]string, len(TradingPeriods))
	for i, period := range TradingPeriods {
		intervals[i] = PeriodToBinanceInterval[period]
	}
	return intervals
}

const (
	PeriodM1  = "M1"
	PeriodM5  = "M5"
	PeriodM15 = "M15"
	PeriodM30 = "M30"
	PeriodH1  = "H1"
	PeriodH4  = "H4"
	PeriodD1  = "D1"
)

const (
	SignalBuy  = "BUY"
	SignalSell = "SELL"
	SignalHold = "HOLD"
)

const (
	TrendingUp   = "trending_up"
	TrendingDown = "trending_down"
	Ranging      = "ranging"
	Breakout     = "breakout"
)

const (
	VolatilityExpanding   = "expanding"
	VolatilityContracting = "contracting"
	VolatilityStable      = "stable"
)

const (
	MomentumRising  = "rising"
	MomentumFalling = "falling"
	MomentumStable  = "stable"
)

const (
	TierNormal     = 0
	TierStrong     = 1
	TierStronger   = 2
	TierVeryStrong = 3
)

const (
	ExecOrderAccepted        = "ORDER_ACCEPTED"
	ExecOrderFilled          = "ORDER_FILLED"
	ExecOrderReplaced        = "ORDER_REPLACED"
	ExecOrderCancelled       = "ORDER_CANCELLED"
	ExecOrderExpired         = "ORDER_EXPIRED"
	ExecOrderRejected        = "ORDER_REJECTED"
	ExecCancelOrderRejected  = "ORDER_CANCEL_REJECTED"
	ExecSwap                 = "SWAP"
	ExecDepositWithdraw      = "DEPOSIT_WITHDRAW"
	ExecOrderPartialFill     = "ORDER_PARTIAL_FILL"
	ExecBonusDepositWithdraw = "BONUS_DEPOSIT_WITHDRAW"
	
	//Custom event
	ExecCloseRejected        = "CLOSE_REJECTED"
)
