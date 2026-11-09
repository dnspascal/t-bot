package config

import (
	"testing"
	"time"
)

func TestSessionOpenFollowsDST(t *testing.T) {
	cases := []struct{ utc, want string }{
		{"2026-10-20T07:00:00Z", "london"}, // BST: 08:00 London
		{"2026-10-20T07:29:00Z", "london"},
		{"2026-10-20T07:30:00Z", ""},
		{"2026-11-09T07:25:00Z", ""}, // GMT: 07:25 London, an hour before the open
		{"2026-11-09T08:00:00Z", "london"},
		{"2026-11-09T08:29:00Z", "london"},
		{"2026-10-20T13:00:00Z", "ny"}, // EDT: 09:00 New York
		{"2026-11-09T13:15:00Z", ""}, // EST: 08:15 New York
		{"2026-11-09T14:00:00Z", "ny"},
		{"2026-11-09T14:30:00Z", ""},
	}
	for _, c := range cases {
		ts, _ := time.Parse(time.RFC3339, c.utc)
		if got := SessionOpen(ts); got != c.want {
			t.Errorf("%s: got %q, want %q", c.utc, got, c.want)
		}
	}
}

func TestPreSessionHour(t *testing.T) {
	cases := []struct{ utc, want string }{
		{"2026-10-20T06:30:00Z", "london"},
		{"2026-11-09T07:30:00Z", "london"},
		{"2026-11-09T06:30:00Z", ""},
		{"2026-11-09T13:30:00Z", "ny"},
		{"2026-10-20T12:30:00Z", "ny"},
	}
	for _, c := range cases {
		ts, _ := time.Parse(time.RFC3339, c.utc)
		if got := PreSessionHour(ts); got != c.want {
			t.Errorf("%s: got %q, want %q", c.utc, got, c.want)
		}
	}
}
