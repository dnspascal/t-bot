package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/denismgaya/t-bot/internal/provider/ctrader/api"
)

var periodCodes = map[string]uint32{"M1": api.PeriodM1, "M5": api.PeriodM5, "M15": api.PeriodM15, "H1": api.PeriodH1}

// fetch downloads closed bars (real closes, GetTrendbars) for each period from `from` until now into dir/<period>.csv.
// Read-only market data; uses the access token as-is and never refreshes it.
func fetch(dir string, from time.Time) error {
	clientID, secret, token := os.Getenv("CTRADER_CLIENT_ID"), os.Getenv("CTRADER_CLIENT_SECRET"), os.Getenv("CTRADER_ACCESS_TOKEN")
	demo := os.Getenv("CTRADER_DEMO") == "true"
	symbolID, _ := strconv.ParseInt(os.Getenv("CTRADER_SYMBOL_ID"), 10, 64)
	if clientID == "" || token == "" || symbolID == 0 {
		return fmt.Errorf("need CTRADER_CLIENT_ID, CTRADER_CLIENT_SECRET, CTRADER_ACCESS_TOKEN, CTRADER_SYMBOL_ID")
	}

	c := api.NewClient(demo, 0, symbolID, 100000, 0.01)
	if err := c.Connect(); err != nil {
		return err
	}
	defer c.Close()
	if err := c.AuthApp(clientID, secret); err != nil {
		return err
	}
	time.Sleep(2 * time.Second)
	accounts, err := c.GetAccountList(token)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		if a.IsLive == !demo {
			c.SetAccountID(a.CtidTraderAccountID)
		}
	}
	if err := c.AuthAccount(token); err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"H1", "M15", "M5", "M1"} {
		seen := map[int64]api.Trendbar{}
		to := time.Now().UnixMilli()
		for {
			bars, err := c.FetchHistoricalTrendbarsFrom(periodCodes[name], 5000, to)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			if len(bars) == 0 {
				break
			}
			oldest := bars[0].OpenTime
			for _, b := range bars {
				seen[b.OpenTime] = b
				oldest = min(oldest, b.OpenTime)
			}
			fmt.Fprintf(os.Stderr, "%s: %d bars, back to %s\n", name, len(seen), time.Unix(oldest, 0).UTC().Format(time.DateTime))
			if oldest <= from.Unix() || oldest*1000 >= to {
				break
			}
			to = oldest * 1000
			time.Sleep(400 * time.Millisecond)
		}
		if err := writeBars(filepath.Join(dir, name+".csv"), seen, from.Unix()); err != nil {
			return err
		}
	}
	return nil
}

func writeBars(path string, m map[int64]api.Trendbar, fromUnix int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	keys := make([]int64, 0, len(m))
	for k := range m {
		if k >= fromUnix {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	w := csv.NewWriter(f)
	for _, k := range keys {
		b := m[k]
		w.Write([]string{strconv.FormatInt(b.OpenTime, 10), ff(b.Open), ff(b.High), ff(b.Low), ff(b.Close), strconv.FormatInt(b.Volume, 10)})
	}
	w.Flush()
	return w.Error()
}

func ff(v float64) string { return strconv.FormatFloat(v, 'f', 5, 64) }
