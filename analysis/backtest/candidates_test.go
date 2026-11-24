package main

import (
	"testing"
	"time"
)

func TestRollovers(t *testing.T) {
	at := func(s string) int64 {
		x, err := time.ParseInLocation("2006-01-02 15:04", s, newYork)
		if err != nil {
			t.Fatal(err)
		}
		return x.Unix()
	}
	for _, tc := range []struct {
		from, to string
		want     int
	}{
		{"2026-07-28 18:05", "2026-07-29 16:50", 0}, // inside one session
		{"2026-07-28 12:00", "2026-07-28 18:30", 1}, // Tuesday 17:00
		{"2026-07-29 12:00", "2026-07-29 18:30", 3}, // Wednesday 17:00 counts 3
		{"2026-07-31 12:00", "2026-08-03 12:00", 1}, // Friday 17:00 only, weekend free
	} {
		if got := rollovers(at(tc.from), at(tc.to)); got != tc.want {
			t.Errorf("%s -> %s: %d rollovers, want %d", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestWeekdayOfMonth(t *testing.T) {
	for _, tc := range []struct {
		d    string
		want int
	}{{"2026-12-01", 1}, {"2026-12-03", 3}, {"2026-12-07", 5}, {"2026-11-02", 1}, {"2026-11-04", 3}} {
		d, _ := time.ParseInLocation(time.DateOnly, tc.d, newYork)
		if got := weekdayOfMonth(d); got != tc.want {
			t.Errorf("%s: weekday %d, want %d", tc.d, got, tc.want)
		}
	}
}
