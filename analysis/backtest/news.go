package main

import (
	"os"
	"strings"
	"time"
)

// BT_NEWS="08:15-08:45,09:15-09:45" blocks entries inside these New York-time windows on weekdays;
// BT_NEWS_CLOSE=1 also closes open positions when a window starts.
var (
	newsWindows = parseNews(os.Getenv("BT_NEWS"))
	newsClose   = os.Getenv("BT_NEWS_CLOSE") != ""
	newYork, _  = time.LoadLocation("America/New_York")
)

func parseNews(v string) [][2]int {
	var out [][2]int
	for _, part := range strings.Split(v, ",") {
		a, b, ok := strings.Cut(part, "-")
		if !ok {
			continue
		}
		out = append(out, [2]int{hhmm(a), hhmm(b)})
	}
	return out
}

func hhmm(s string) int {
	t, err := time.Parse("15:04", s)
	must(err)
	return t.Hour()*60 + t.Minute()
}

func nyMinute(t int64) (int, bool) {
	ny := time.Unix(t, 0).In(newYork)
	return ny.Hour()*60 + ny.Minute(), ny.Weekday() != time.Saturday && ny.Weekday() != time.Sunday
}

func inNews(t int64) bool {
	m, weekday := nyMinute(t)
	for _, w := range newsWindows {
		if weekday && m >= w[0] && m < w[1] {
			return true
		}
	}
	return false
}

func newsStarts(t int64) bool {
	m, weekday := nyMinute(t)
	for _, w := range newsWindows {
		if newsClose && weekday && m == w[0] {
			return true
		}
	}
	return false
}
