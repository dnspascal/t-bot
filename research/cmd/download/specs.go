package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/denismgaya/t-bot/internal/provider/ctrader/api"
)

// ProtoOASymbol field numbers worth reading (volumes are in cents of a unit: 100 = 1 unit).
var specFields = map[int]string{
	2: "digits", 3: "pipPosition", 9: "maxVolume", 10: "minVolume", 11: "stepVolume",
	15: "commissionType", 21: "minCommission", 22: "minCommissionType", 30: "lotSize",
	31: "preciseTradingCommissionRate", 32: "preciseMinCommission",
}

var dumpLine = regexp.MustCompile(`field=(\d+) wire=varint value=(\d+)`)

// printSpecs prints contract details for the symbols and samples live bid/ask for sample seconds each.
func printSpecs(symbols map[string]int64, sample time.Duration) error {
	c, err := connect()
	if err != nil {
		return err
	}
	defer c.Close()

	names := make([]string, 0, len(symbols))
	for n := range symbols {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, name := range names {
		fields, err := symbolFields(c, symbols[name])
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		fmt.Printf("\n== %s (id %d)\n", name, symbols[name])
		nums := make([]int, 0, len(fields))
		for f := range fields {
			nums = append(nums, f)
		}
		slices.Sort(nums)
		for _, f := range nums {
			if label, ok := specFields[f]; ok {
				fmt.Printf("  %-30s %d\n", label, fields[f])
			}
		}
		if lot, minV := fields[30], fields[10]; lot > 0 {
			fmt.Printf("  -> 1 lot = %.2f units, minimum trade = %.2f units (%.4f lot)\n",
				float64(lot)/100, float64(minV)/100, float64(minV)/float64(lot))
		}
	}

	for _, name := range names {
		if err := sampleSpread(c, name, symbols[name], sample); err != nil {
			return err
		}
	}
	return nil
}

func symbolFields(c *api.Client, id int64) (map[int]uint64, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stderr := os.Stderr
	os.Stderr = w
	api.DebugDumpSymbolFields = true
	_, err = c.GetSymbolsByIds([]int64{id})
	api.DebugDumpSymbolFields = false
	os.Stderr = stderr
	w.Close()
	fields := map[int]uint64{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if m := dumpLine.FindStringSubmatch(sc.Text()); m != nil {
			f, _ := strconv.Atoi(m[1])
			v, _ := strconv.ParseUint(m[2], 10, 64)
			fields[f] = v
		}
	}
	r.Close()
	return fields, err
}

func sampleSpread(c *api.Client, name string, id int64, d time.Duration) error {
	c.SetSymbolID(id)
	if err := c.SubscribeSpots(); err != nil {
		return err
	}
	fmt.Printf("\n%s: sampling bid/ask for %s ...\n", name, d)
	var spreads []float64
	var bid float64
	end := time.After(d)
	for done := false; !done; {
		select {
		case e := <-c.PriceCh:
			if e.Bid > 0 && e.Ask > 0 {
				spreads = append(spreads, e.Ask-e.Bid)
				bid = e.Bid
			}
		case <-end:
			done = true
		}
	}
	if len(spreads) == 0 {
		fmt.Printf("  no prices received (market closed?)\n")
		return nil
	}
	slices.Sort(spreads)
	sum := 0.0
	for _, s := range spreads {
		sum += s
	}
	avg := sum / float64(len(spreads))
	fmt.Printf("  %d ticks, bid %.3f, spread avg %.4f  median %.4f  max %.4f  (%.4f%% of price)\n",
		len(spreads), bid, avg, spreads[len(spreads)/2], spreads[len(spreads)-1], 100*avg/bid)
	return nil
}
