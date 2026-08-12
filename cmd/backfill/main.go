package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/denismgaya/t-bot/internal/bot"
	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/provider/ctrader/api"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SymbolStruct struct {
	name string
	id   int64
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal("pgxpool.New", err)
	}
	defer db.Close()

	token, err := bot.LoadCredential(ctx, db, "ctrader_access_token")
	if err != nil {
		log.Fatal("load access token:", err)
	}

	refreshToken, err := bot.LoadCredential(ctx, db, "ctrader_refresh_token")
	if err != nil {
		log.Fatal("load refresh token:", err)
	}

	newAccess, newRefresh, err := api.RefreshToken(cfg.CTrader.ClientID, cfg.CTrader.ClientSecret, refreshToken)
	if err != nil {
		log.Fatal("refresh token:", err)
	}
	token = newAccess
	bot.SaveCredential(ctx, db, "ctrader_access_token", newAccess)
	bot.SaveCredential(ctx, db, "ctrader_refresh_token", newRefresh)

	symbols := []SymbolStruct{
		{name: "EURUSD", id: 1},
		{name: "XAUUSD", id: 41},
	}

	periods := []uint32{api.PeriodM5, api.PeriodM15, api.PeriodH1}

	cutoff := time.Now().AddDate(-1, 0, 0).Unix() // seconds, matches Trendbar.OpenTime

	for _, s := range symbols {

		conn := api.NewClient(cfg.CTrader.Demo, cfg.CTrader.AccountID, s.id, 100000, 0.0001)
		if err := conn.Connect(); err != nil {
			log.Fatal("Connect:", err)
		}

		if err := conn.AuthApp(cfg.CTrader.ClientID, cfg.CTrader.ClientSecret); err != nil {
			log.Fatal("AuthApp:", err)
		}
		time.Sleep(2 * time.Second)

		accounts, err := conn.GetAccountList(token)
		if err != nil {
			log.Fatal("GetAccountList:", err)
		}
		for _, acc := range accounts {
			if acc.IsLive == !cfg.CTrader.Demo {
				conn.SetAccountID(acc.CtidTraderAccountID)
				break
			}
		}

		if err := conn.AuthAccount(token); err != nil {
			log.Fatal("AuthAccount:", err)
		}

		f, err := os.Create(s.name + ".csv")
		if err != nil {
			log.Fatal("Create CSV file:", err)
		}
		w := csv.NewWriter(f)
		w.Write([]string{"symbol", "period", "open_time", "open", "high", "low", "close", "volume"})

		for _, p := range periods {
			log.Printf("Backfilling %s %s", s.name, api.PeriodToString(p))

			var allBars []api.Trendbar
			toMs := time.Now().UnixMilli()

			for {
				chunk, err := conn.FetchHistoricalTrendbarsFrom(p, 13999, toMs)
				if err != nil {
					log.Fatal("fetch:", err)
				}
				allBars = append(allBars, chunk...)
				log.Printf("fetched %d bars, total %d", len(chunk), len(allBars))

				if len(chunk) == 0 || chunk[0].OpenTime <= cutoff {
					break
				}
				toMs = chunk[0].OpenTime * 1000 // OpenTime is seconds, API expects ms
			}

			for _, bar := range allBars {
				w.Write([]string{
					s.name,
					api.PeriodToString(p),
					fmt.Sprintf("%d", bar.OpenTime),
					fmt.Sprintf("%f", bar.Open),
					fmt.Sprintf("%f", bar.High),
					fmt.Sprintf("%f", bar.Low),
					fmt.Sprintf("%f", bar.Close),
					fmt.Sprintf("%d", bar.Volume),
				})
			}
		}
		w.Flush()
		f.Close()
		conn.Close()
	}

}
