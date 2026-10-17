package bot

import (
	"testing"
	"time"
)

func TestSessionSkipReason(t *testing.T) {
	cases := []struct {
		name   string
		symbol string
		utc    string
		want   string
	}{
		{"BST before London 08:00", "XAUUSD", "2026-10-20T06:59:00Z", ""},
		{"BST London 08:00", "XAUUSD", "2026-10-20T07:00:00Z", SkipReasonLondonSession},
		{"BST London 12:59", "XAUUSD", "2026-10-20T11:59:00Z", SkipReasonLondonSession},
		{"BST London 13:00", "XAUUSD", "2026-10-20T12:00:00Z", ""},
		{"GMT before London 08:00", "XAUUSD", "2026-10-27T07:59:00Z", ""},
		{"GMT London 08:00", "XAUUSD", "2026-10-27T08:00:00Z", SkipReasonLondonSession},
		{"GMT London 12:59", "XAUUSD", "2026-10-27T12:59:00Z", SkipReasonLondonSession},
		{"GMT London 13:00", "XAUUSD", "2026-10-27T13:00:00Z", ""},
		{"other symbol never blocked", "EURUSD", "2026-10-20T09:00:00Z", ""},
	}
	for _, c := range cases {
		now, err := time.Parse(time.RFC3339, c.utc)
		if err != nil {
			t.Fatal(err)
		}
		if got := sessionSkipReason(c.symbol, now); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
