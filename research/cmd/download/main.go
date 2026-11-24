package main

import (
	"encoding/csv"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/denismgaya/t-bot/internal/provider/ctrader/api"
)

var periodCodes = map[string]uint32{"M1": api.PeriodM1, "M5": api.PeriodM5, "M15": api.PeriodM15, "M30": api.PeriodM30, "H1": api.PeriodH1, "H4": api.PeriodH4, "D1": api.PeriodD1}

// timeframes is the download order: small files first so a bad connection shows up quickly.
var timeframes = []string{"D1", "H4", "H1", "M30", "M15", "M5", "M1"}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "specs" {
		if err := printSpecs(map[string]int64{"XAUUSD": 41, oilSymbol: 10054}, 45*time.Second); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	root := "research/data"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	logPath := filepath.Join(root, "logs", "download_"+time.Now().Format("2006-01-02_150405")+".log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	logFile, err := os.Create(logPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer logFile.Close()
	slog.SetDefault(slog.New(slog.NewTextHandler(logFile, nil)))
	fmt.Println("cTrader client log:", logPath)

	if err := downloadAll(root, os.Args[min(2, len(os.Args)):]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// connect opens an authenticated read-only cTrader session; uses the access token as-is and never refreshes it.
func connect() (*api.Client, error) {
	clientID, secret, token := os.Getenv("CTRADER_CLIENT_ID"), os.Getenv("CTRADER_CLIENT_SECRET"), os.Getenv("CTRADER_ACCESS_TOKEN")
	demo := os.Getenv("CTRADER_DEMO") == "true"
	if clientID == "" || token == "" {
		return nil, fmt.Errorf("need CTRADER_CLIENT_ID, CTRADER_CLIENT_SECRET, CTRADER_ACCESS_TOKEN")
	}
	c := api.NewClient(demo, 0, 0, 100000, 0.01)
	if err := c.Connect(); err != nil {
		return nil, err
	}
	if err := c.AuthApp(clientID, secret); err != nil {
		c.Close()
		return nil, err
	}
	time.Sleep(2 * time.Second)
	accounts, err := c.GetAccountList(token)
	if err != nil {
		c.Close()
		return nil, err
	}
	for _, a := range accounts {
		if a.IsLive == !demo {
			c.SetAccountID(a.CtidTraderAccountID)
		}
	}
	if err := c.AuthAccount(token); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
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
	slices.Sort(keys)
	w := csv.NewWriter(f)
	for _, k := range keys {
		b := m[k]
		w.Write([]string{strconv.FormatInt(b.OpenTime, 10), ff(b.Open), ff(b.High), ff(b.Low), ff(b.Close), strconv.FormatInt(b.Volume, 10)})
	}
	w.Flush()
	return w.Error()
}

func ff(v float64) string { return strconv.FormatFloat(v, 'f', 5, 64) }
