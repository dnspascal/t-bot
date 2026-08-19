package strategy

import "strings"

const (
	CloseReasonSLHit       = "sl_hit"
	CloseReasonTPHit       = "tp_hit"
	CloseReasonBreakevenSL = "breakeven_sl"
	CloseReasonEODClose    = "eod_close"

	PeakDrawbackPrefix = "peak_drawback="
	TimeStopPrefix     = "time_stop="

	CloseReasonTimeStopNeverProfitable30m = TimeStopPrefix + "never_profitable_30m"
	CloseReasonTimeStopStalled60m         = TimeStopPrefix + "stalled_60m"

	ReversalRegimeAgainst   = "regime_against"
	ReversalRSIAgainst      = "rsi_against"
	ReversalEMACrossAgainst = "ema_cross_against"
	ReversalMomentumAgainst = "momentum_against"
)

type CloseOutcome int

const (
	CloseInvalidated CloseOutcome = iota
	CloseValidated
	CloseNeutral
)

func ClassifyCloseReason(reason string) CloseOutcome {
	switch {
	case reason == CloseReasonSLHit:
		return CloseInvalidated
	case strings.HasPrefix(reason, TimeStopPrefix):
		return CloseInvalidated
	case reason == CloseReasonEODClose:
		return CloseNeutral
	case reason == CloseReasonTPHit,
		reason == CloseReasonBreakevenSL,
		strings.HasPrefix(reason, PeakDrawbackPrefix):
		return CloseValidated
	}
	for _, tok := range []string{ReversalRegimeAgainst, ReversalRSIAgainst, ReversalEMACrossAgainst, ReversalMomentumAgainst} {
		if strings.Contains(reason, tok) {
			return CloseInvalidated
		}
	}
	return CloseNeutral
}
