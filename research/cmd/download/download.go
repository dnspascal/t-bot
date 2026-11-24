package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/denismgaya/t-bot/internal/provider/ctrader/api"
)

// oilSymbol is the broker's name for WTI crude; if the lookup fails it prints the oil-like names to put here.
const oilSymbol = "SpotCrude"

// maxRetries is how many times a failed page is retried on a fresh connection before the download gives up.
const maxRetries = 6

type job struct {
	key    string
	symbol string
	id     int64
	from   time.Time
	label  string
}

type progress struct {
	mu    sync.Mutex
	order []string
	lines map[string]string
}

func (p *progress) set(key, format string, args ...any) {
	p.mu.Lock()
	p.lines[key] = fmt.Sprintf(format, args...)
	p.mu.Unlock()
}

func (p *progress) draw(first bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !first {
		fmt.Printf("\033[%dA", len(p.order))
	}
	for _, k := range p.order {
		line := fmt.Sprintf("%-9s %s", k, p.lines[k])
		if r := []rune(line); len(r) > 100 {
			line = string(r[:99]) + "…"
		}
		fmt.Printf("\033[2K%s\n", line)
	}
}

func downloadGold6y(root string, p *progress) error {
	return downloadSymbol(root, job{"gold_6y", "XAUUSD", 41, day("2021-01-01"), "copy2"}, p)
}

func downloadGold10y(root string, p *progress) error {
	return downloadSymbol(root, job{"gold_10y", "XAUUSD", 41, day("2016-01-01"), ""}, p)
}

func downloadOil10y(root string, p *progress) error {
	p.set("oil_10y", "looking up %s", oilSymbol)
	id, err := symbolID(oilSymbol)
	if err != nil {
		return err
	}
	return downloadSymbol(root, job{"oil_10y", oilSymbol, id, day("2016-01-01"), ""}, p)
}

// byName downloads 10 years of a symbol looked up by its broker name.
func byName(key, name string) func(string, *progress) error {
	return func(root string, p *progress) error {
		p.set(key, "looking up %s", name)
		id, err := symbolID(name)
		if err != nil {
			return err
		}
		return downloadSymbol(root, job{key, name, id, day("2016-01-01"), ""}, p)
	}
}

func downloadAll(root string, only []string) error {
	funcs := map[string]func(string, *progress) error{"gold_6y": downloadGold6y, "gold_10y": downloadGold10y, "oil_10y": downloadOil10y,
		"eurusd_10y": byName("eurusd_10y", "EURUSD"), "xagusd_10y": byName("xagusd_10y", "XAGUSD")}
	order := []string{"gold_6y", "gold_10y", "oil_10y"}
	if len(only) > 0 {
		order = only
	}
	for _, k := range order {
		if funcs[k] == nil {
			return fmt.Errorf("unknown download %q (have gold_6y, gold_10y, oil_10y, eurusd_10y, xagusd_10y)", k)
		}
	}
	p := &progress{order: order, lines: map[string]string{}}
	errs := map[string]error{}
	var wg sync.WaitGroup
	var emu sync.Mutex
	for _, k := range p.order {
		p.set(k, "starting")
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			err := funcs[k](root, p)
			if err != nil {
				p.set(k, "FAILED: %v", err)
			}
			emu.Lock()
			errs[k] = err
			emu.Unlock()
		}(k)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	p.draw(true)
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			p.draw(false)
		case <-done:
			p.draw(false)
			var failed []string
			for _, k := range p.order {
				if errs[k] != nil {
					failed = append(failed, k)
				}
			}
			if len(failed) > 0 {
				return fmt.Errorf("failed: %s", strings.Join(failed, ", "))
			}
			return nil
		}
	}
}

func downloadSymbol(root string, j job, p *progress) error {
	dir := filepath.Join(root, "raw", "ctrader", j.symbol, time.Now().Format(time.DateOnly)+"_from"+j.from.Format("2006"))
	if j.label != "" {
		dir += "_" + j.label
	}
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s already exists", dir)
	}
	tmp := dir + ".partial"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	p.set(j.key, "connecting")
	c, err := connect()
	if err != nil {
		return err
	}
	defer func() {
		if c != nil {
			c.Close()
		}
	}()
	c.SetSymbolID(j.id)

	now := time.Now()
	for _, tf := range timeframes {
		bars := map[int64]api.Trendbar{}
		to := now.UnixMilli()
		for {
			page, err := c.FetchHistoricalTrendbarsFrom(periodCodes[tf], 5000, to)
			for try := 1; err != nil && try <= maxRetries; try++ {
				wait := time.Duration(try*10) * time.Second
				p.set(j.key, "%s %-3s retry %d/%d in %s after: %v", j.symbol, tf, try, maxRetries, wait, err)
				if c != nil {
					c.Close()
				}
				time.Sleep(wait)
				if c, err = connect(); err != nil {
					continue
				}
				c.SetSymbolID(j.id)
				page, err = c.FetchHistoricalTrendbarsFrom(periodCodes[tf], 5000, to)
			}
			if err != nil {
				return fmt.Errorf("%s: %w", tf, err)
			}
			if len(page) == 0 {
				break
			}
			oldest := page[0].OpenTime
			for _, b := range page {
				bars[b.OpenTime] = b
				oldest = min(oldest, b.OpenTime)
			}
			pct := 100 * float64(now.Unix()-oldest) / float64(now.Unix()-j.from.Unix())
			p.set(j.key, "%s %-3s %5.1f%%  %8d bars  back to %s", j.symbol, tf, min(pct, 100), len(bars),
				time.Unix(oldest, 0).UTC().Format(time.DateOnly))
			if oldest <= j.from.Unix() || oldest*1000 >= to {
				break
			}
			to = oldest * 1000
			time.Sleep(400 * time.Millisecond)
		}
		if err := writeBars(filepath.Join(tmp, tf+".csv"), bars, j.from.Unix()); err != nil {
			return err
		}
	}
	if err := writeManifest(tmp, j); err != nil {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return err
	}
	p.set(j.key, "done -> %s", dir)
	return nil
}

func symbolID(name string) (int64, error) {
	c, err := connect()
	if err != nil {
		return 0, err
	}
	defer c.Close()
	syms, err := c.ListSymbols()
	if err != nil {
		return 0, err
	}
	var similar []string
	for _, s := range syms {
		if strings.EqualFold(s.SymbolName, name) {
			return s.SymbolID, nil
		}
		for _, w := range []string{"OIL", "CRUDE", "WTI", "BRENT", "XAG", "SILVER", "EUR"} {
			if strings.Contains(strings.ToUpper(s.SymbolName), w) {
				similar = append(similar, fmt.Sprintf("%s=%d", s.SymbolName, s.SymbolID))
				break
			}
		}
	}
	return 0, fmt.Errorf("no symbol %q; similar: %s", name, strings.Join(similar, " "))
}

type fileInfo struct {
	Rows   int    `json:"rows"`
	First  string `json:"first"`
	Last   string `json:"last"`
	SHA256 string `json:"sha256"`
}

func writeManifest(dir string, j job) error {
	files := map[string]fileInfo{}
	for _, tf := range timeframes {
		info, err := describe(filepath.Join(dir, tf+".csv"))
		if err != nil {
			return err
		}
		files[tf] = info
	}
	m := map[string]any{"source": "ctrader", "symbol": j.symbol, "symbol_id": j.id, "from": j.from.Format(time.DateOnly),
		"downloaded_at": time.Now().UTC().Format(time.RFC3339), "files": files}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o644)
}

func describe(path string) (fileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return fileInfo{}, err
	}
	defer f.Close()
	h := sha256.New()
	data, err := io.ReadAll(io.TeeReader(f, h))
	if err != nil {
		return fileInfo{}, err
	}
	info := fileInfo{SHA256: hex.EncodeToString(h.Sum(nil))}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return info, nil
	}
	lines := strings.Split(text, "\n")
	info.Rows = len(lines)
	info.First, info.Last = unixField(lines[0]), unixField(lines[len(lines)-1])
	return info, nil
}

func unixField(line string) string {
	var ts int64
	fmt.Sscan(strings.SplitN(line, ",", 2)[0], &ts)
	return time.Unix(ts, 0).UTC().Format(time.RFC3339)
}

func day(s string) time.Time {
	t, _ := time.Parse(time.DateOnly, s)
	return t
}
