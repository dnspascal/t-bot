package main

// Real multi-week daily-range comparison: XAUUSD vs XAGUSD/XPDUSD/XPTUSD,
// pulled directly from the broker (D1 trendbars, ~30 days), not a one-day
// platform snapshot. Reuses one authenticated session across all four via
// Client.SetSymbolID -- read-only, no orders.
//
// Run: go run . <client_id> <client_secret> <access_token> <demo:true|false>
import (
	"fmt"
	"os"
	"time"

	"github.com/denismgaya/t-bot/internal/provider/ctrader/api"
)

type target struct {
	name string
	id   int64
}

func main() {
	if len(os.Args) < 5 {
		fmt.Fprintln(os.Stderr, "usage: metals_volatility_compare <client_id> <client_secret> <access_token> <demo:true|false>")
		os.Exit(1)
	}
	clientID, clientSecret, accessToken := os.Args[1], os.Args[2], os.Args[3]
	demo := os.Args[4] == "true"

	c := api.NewClient(demo, 0, 41, 100000, 0.01)

	must(c.Connect())
	defer c.Close()
	must(c.AuthApp(clientID, clientSecret))
	time.Sleep(2 * time.Second)

	accounts, err := c.GetAccountList(accessToken)
	must(err)
	var ctidAccountID int64
	for _, a := range accounts {
		if a.IsLive == !demo {
			ctidAccountID = a.CtidTraderAccountID
		}
	}
	c.SetAccountID(ctidAccountID)
	must(c.AuthAccount(accessToken))
	fmt.Fprintln(os.Stderr, "authenticated, fetching D1 trendbars per symbol...")

	targets := []target{
		{"XAUUSD", 41},
		{"XAGUSD", 64},
		{"XPDUSD", 73},
		{"XPTUSD", 74},
	}

	fmt.Printf("%-8s %8s %10s %10s %10s %10s\n", "symbol", "days", "avg_range", "avg_range%", "max_range", "max_range%")
	for _, t := range targets {
		c.SetSymbolID(t.id)
		bars, err := c.FetchHistoricalTrendbars(api.PeriodD1, 30)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: fetch failed: %v\n", t.name, err)
			continue
		}
		if len(bars) == 0 {
			fmt.Fprintf(os.Stderr, "  %s: no bars returned\n", t.name)
			continue
		}
		var sumRange, sumRangePct, maxRange, maxRangePct float64
		n := 0
		for _, b := range bars {
			if b.Close <= 0 {
				continue
			}
			rng := b.High - b.Low
			rngPct := 100 * rng / b.Close
			sumRange += rng
			sumRangePct += rngPct
			if rng > maxRange {
				maxRange = rng
			}
			if rngPct > maxRangePct {
				maxRangePct = rngPct
			}
			n++
		}
		if n == 0 {
			continue
		}
		fmt.Printf("%-8s %8d %10.4f %9.2f%% %10.4f %9.2f%%\n", t.name, n, sumRange/float64(n), sumRangePct/float64(n), maxRange, maxRangePct)
		time.Sleep(500 * time.Millisecond) // be gentle between symbol requests
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
