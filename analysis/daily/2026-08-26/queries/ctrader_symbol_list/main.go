package main

// Read-only, exploratory: connects to Pepperstone via cTrader Open API using
// the existing, already-proven api.Client (same Connect/AuthApp/AuthAccount/
// GetSymbolsByIds path the live bot uses), but calls GetSymbolsByIds with an
// EMPTY id list instead of specific ids -- testing whether this broker's
// server treats "no ids" as "list everything" on the same payload type
// (2116) that already works for by-id lookups. No orders, no state changes.
//
// Run: go run . <client_id> <client_secret> <access_token> <account_id> <demo:true|false>
import (
	"fmt"
	"os"
	"time"

	"github.com/denismgaya/t-bot/internal/provider/ctrader/api"
)

func main() {
	if len(os.Args) < 6 {
		fmt.Fprintln(os.Stderr, "usage: ctrader_symbol_list <client_id> <client_secret> <access_token> <account_id> <demo:true|false>")
		os.Exit(1)
	}
	clientID, clientSecret, accessToken := os.Args[1], os.Args[2], os.Args[3]
	_ = os.Args[4] // unused now — account id is resolved via GetAccountList, not trusted from env
	demo := os.Args[5] == "true"

	c := api.NewClient(demo, 0, 1, 100000, 0.0001)

	fmt.Fprintln(os.Stderr, "connecting...")
	if err := c.Connect(); err != nil {
		fmt.Fprintln(os.Stderr, "connect failed:", err)
		os.Exit(1)
	}
	defer c.Close()

	fmt.Fprintln(os.Stderr, "app auth...")
	if err := c.AuthApp(clientID, clientSecret); err != nil {
		fmt.Fprintln(os.Stderr, "app auth send failed:", err)
		os.Exit(1)
	}
	time.Sleep(2 * time.Second)

	fmt.Fprintln(os.Stderr, "resolving account id via GetAccountList...")
	accounts, err := c.GetAccountList(accessToken)
	if err != nil {
		fmt.Fprintln(os.Stderr, "GetAccountList failed:", err)
		os.Exit(1)
	}
	var ctidAccountID int64
	for _, a := range accounts {
		fmt.Fprintf(os.Stderr, "  found account: ctid=%d live=%v login=%d broker=%s\n", a.CtidTraderAccountID, a.IsLive, a.TraderLogin, a.BrokerName)
		if a.IsLive == !demo {
			ctidAccountID = a.CtidTraderAccountID
		}
	}
	if ctidAccountID == 0 {
		fmt.Fprintln(os.Stderr, "no matching account found for demo =", demo)
		os.Exit(1)
	}
	c.SetAccountID(ctidAccountID)

	fmt.Fprintln(os.Stderr, "account auth...")
	if err := c.AuthAccount(accessToken); err != nil {
		fmt.Fprintln(os.Stderr, "account auth failed:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "account authed, requesting id=41 and id=42 with field dump...")
	api.DebugDumpSymbolFields = true
	syms, err := c.GetSymbolsByIds([]int64{41, 42})
	if err != nil {
		fmt.Fprintln(os.Stderr, "GetSymbolsByIds failed:", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "got %d symbols\n\n", len(syms))
	for _, s := range syms {
		fmt.Printf("%-8d %s\n", s.SymbolID, s.SymbolName)
	}
}
