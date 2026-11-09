package config

import (
	"time"
	_ "time/tzdata"
)

var (
	LondonLoc  = mustLoadLocation("Europe/London")
	NewYorkLoc = mustLoadLocation("America/New_York")
)

// Session open windows in local exchange time, so DST shifts are handled.
const (
	LondonOpenHour  = 8 // 08:00-08:30 London
	NewYorkOpenHour = 9 // 09:00-09:30 New York
	SessionOpenMins = 30
)

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// SessionOpen reports which session-open window t falls in: "london", "ny" or "".
func SessionOpen(t time.Time) string {
	if l := t.In(LondonLoc); l.Hour() == LondonOpenHour && l.Minute() < SessionOpenMins {
		return "london"
	}
	if n := t.In(NewYorkLoc); n.Hour() == NewYorkOpenHour && n.Minute() < SessionOpenMins {
		return "ny"
	}
	return ""
}

// PreSessionHour reports which session t is the hour before: "london", "ny" or "".
func PreSessionHour(t time.Time) string {
	if t.In(LondonLoc).Hour() == LondonOpenHour-1 {
		return "london"
	}
	if t.In(NewYorkLoc).Hour() == NewYorkOpenHour-1 {
		return "ny"
	}
	return ""
}
