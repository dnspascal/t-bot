package bot

import (
	"time"
	_ "time/tzdata"
)

const SkipReasonLondonSession = "london_session"

type sessionBlock struct {
	reason    string
	loc       *time.Location
	startHour int
	endHour   int
}

var londonLoc = mustLoadLocation("Europe/London")

// Windows where entry signals are recorded with a skip_reason but no order is sent.
// To block XAUUSD London again: "XAUUSD": {{reason: SkipReasonLondonSession, loc: londonLoc, startHour: 8, endHour: 13}}
var sessionBlocks = map[string][]sessionBlock{}

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func sessionSkipReason(symbol string, now time.Time) string {
	for _, blk := range sessionBlocks[symbol] {
		h := now.In(blk.loc).Hour()
		if h >= blk.startHour && h < blk.endHour {
			return blk.reason
		}
	}
	return ""
}
