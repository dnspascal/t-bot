package main

// Pearson correlation of daily returns: XAUUSD vs XAGUSD/XPDUSD/XPTUSD, over
// the same real ~30-day window, matched by actual calendar date (not
// assumed index alignment). High correlation with gold = redundant exposure,
// not real diversification. Read-only, reuses one authenticated session.
//
// Run: go run . <client_id> <client_secret> <access_token> <demo:true|false>
import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/denismgaya/t-bot/internal/provider/ctrader/api"
)

type target struct {
	name string
	id   int64
}

func dailyReturns(bars []api.Trendbar) map[string]float64 {
	out := map[string]float64{}
	// sort not guaranteed by API; build by OpenTime day key, then diff chronologically
	type point struct {
		day   string
		close float64
	}
	pts := []point{}
	seen := map[string]bool{}
	for _, b := range bars {
		day := time.UnixMilli(b.OpenTime * 1000).UTC().Format("2006-01-02")
		if b.OpenTime > 1e12 { // already ms
			day = time.UnixMilli(b.OpenTime).UTC().Format("2006-01-02")
		}
		if seen[day] {
			continue
		}
		seen[day] = true
		pts = append(pts, point{day, b.Close})
	}
	for i := 1; i < len(pts); i++ {
		if pts[i-1].close == 0 {
			continue
		}
		out[pts[i].day] = (pts[i].close - pts[i-1].close) / pts[i-1].close
	}
	return out
}

func pearson(a, b map[string]float64) (float64, int) {
	var xs, ys []float64
	for day, av := range a {
		if bv, ok := b[day]; ok {
			xs = append(xs, av)
			ys = append(ys, bv)
		}
	}
	n := len(xs)
	if n < 3 {
		return 0, n
	}
	var sumX, sumY float64
	for i := range xs {
		sumX += xs[i]
		sumY += ys[i]
	}
	meanX, meanY := sumX/float64(n), sumY/float64(n)
	var num, denX, denY float64
	for i := range xs {
		dx, dy := xs[i]-meanX, ys[i]-meanY
		num += dx * dy
		denX += dx * dx
		denY += dy * dy
	}
	if denX == 0 || denY == 0 {
		return 0, n
	}
	return num / math.Sqrt(denX*denY), n
}

func main() {
	if len(os.Args) < 5 {
		fmt.Fprintln(os.Stderr, "usage: metals_correlation <client_id> <client_secret> <access_token> <demo:true|false>")
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

	returns := map[string]map[string]float64{}
	for _, t := range targets {
		c.SetSymbolID(t.id)
		bars, err := c.FetchHistoricalTrendbars(api.PeriodD1, 45)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: fetch failed: %v\n", t.name, err)
			continue
		}
		returns[t.name] = dailyReturns(bars)
		fmt.Fprintf(os.Stderr, "  %s: %d bars -> %d daily returns\n", t.name, len(bars), len(returns[t.name]))
		time.Sleep(500 * time.Millisecond)
	}

	fmt.Printf("\n%-10s %8s %12s\n", "vs_XAUUSD", "matched_days", "correlation")
	for _, name := range []string{"XAGUSD", "XPDUSD", "XPTUSD"} {
		r, n := pearson(returns["XAUUSD"], returns[name])
		fmt.Printf("%-10s %8d %12.3f\n", name, n, r)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
