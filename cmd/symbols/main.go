package main

import (
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/denismgaya/t-bot/internal/config"
	"github.com/denismgaya/t-bot/internal/provider/ctrader/api"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("load config:", err)
	}
	if !cfg.EnableCTrader {
		log.Fatal("cTrader not enabled in config")
	}
	if cfg.CTrader.RefreshToken == "" {
		log.Fatal("CTRADER_REFRESH_TOKEN is empty")
	}

	accessToken, _, err := api.RefreshToken(cfg.CTrader.ClientID, cfg.CTrader.ClientSecret, cfg.CTrader.RefreshToken)
	if err != nil {
		fmt.Println("Refresh token expired — starting OAuth login in browser...")
		// Always use localhost for local dev OAuth — add http://localhost:8099/callback
		// to your cTrader developer app's allowed redirect URIs.
		accessToken, _, err = api.InitiateOAuthFlow(
			cfg.CTrader.ClientID, cfg.CTrader.ClientSecret,
			"http://localhost:8099/callback", 8099,
		)
		if err != nil {
			log.Fatal("oauth flow:", err)
		}
	}
	fmt.Println("Token OK")

	client := api.NewClient(cfg.CTrader.Demo, cfg.CTrader.AccountID, cfg.CTrader.SymbolID, 100000.0)
	if err := client.Connect(); err != nil {
		log.Fatal("connect:", err)
	}
	defer client.Close()

	if err := client.AuthApp(cfg.CTrader.ClientID, cfg.CTrader.ClientSecret); err != nil {
		log.Fatal("auth app:", err)
	}
	time.Sleep(2 * time.Second)

	accounts, err := client.GetAccountList(accessToken)
	if err != nil {
		log.Fatal("get account list:", err)
	}

	var ctidAccountID int64
	for _, acc := range accounts {
		if acc.IsLive == !cfg.CTrader.Demo {
			ctidAccountID = acc.CtidTraderAccountID
			fmt.Printf("Account: ctidID=%d login=%d broker=%s\n\n", acc.CtidTraderAccountID, acc.TraderLogin, acc.BrokerName)
			break
		}
	}
	if ctidAccountID == 0 {
		log.Fatal("no matching account found")
	}
	client.SetAccountID(ctidAccountID)

	if err := client.AuthAccount(accessToken); err != nil {
		log.Fatal("auth account:", err)
	}

	// Scan IDs in batches until we find the range with valid symbols.
	// ProtoOASymbolsListReq (2116) works as a by-ID lookup on this server;
	// SYMBOL_NOT_FOUND returns immediately so empty batches are fast.
	const batchSize = 500
	const maxID = 50000
	var symbols []api.LightSymbol
	for start := int64(1); start <= maxID; start += batchSize {
		ids := make([]int64, batchSize)
		for i := range ids {
			ids[i] = start + int64(i)
		}
		batch, err := client.GetSymbolsByIds(ids)
		if err != nil {
			log.Fatal("get symbols:", err)
		}
		if len(batch) > 0 {
			fmt.Printf("Found %d symbols starting around ID %d\n", len(batch), start)
			symbols = append(symbols, batch...)
			break
		}
		fmt.Printf("IDs %d-%d: not found\n", start, start+batchSize-1)
	}

	sort.Slice(symbols, func(i, j int) bool {
		return symbols[i].SymbolName < symbols[j].SymbolName
	})

	fmt.Printf("%-10s %-30s %s\n", "ID", "Name", "Enabled")
	fmt.Println("--------------------------------------------------")
	for _, s := range symbols {
		if s.SymbolName == "" {
			continue
		}
		fmt.Printf("%-10d %-30s %v\n", s.SymbolID, s.SymbolName, s.Enabled)
	}
	fmt.Printf("\nTotal with names: %d\n", len(symbols))
}
