// closetest: diagnostic command that opens a 1-micro-lot BUY on EURUSD (demo),
// immediately tries to close it, and logs every step including raw bytes.
// If close is rejected with INCORRECT_BOUNDARIES it retries with shifted field
// numbers (posID→field4, vol→field5) to test the proto-shift hypothesis.
package main

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/denismgaya/t-bot/internal/provider/ctrader/api"
	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		slog.Warn("no .env file", "err", err)
	}

	clientID := mustEnv("CTRADER_CLIENT_ID")
	clientSecret := mustEnv("CTRADER_CLIENT_SECRET")
	accessToken := mustEnv("CTRADER_ACCESS_TOKEN") //nolint:ineffassign — replaced after refresh
	symbolID := mustEnvInt64("CTRADER_SYMBOL_ID")
	demo := os.Getenv("CTRADER_DEMO") != "false"

	slog.Info("closetest starting", "demo", demo, "symbolID", symbolID)

	// Use accountID=0 initially; we'll discover the real ID from GetAccountList.
	client := api.NewClient(demo, 0, symbolID, 100000.0)

	if err := client.Connect(); err != nil {
		log.Fatal("connect:", err)
	}
	defer client.Close()

	// Refresh token first — the stored access token may be expired
	refreshToken := mustEnv("CTRADER_REFRESH_TOKEN")
	slog.Info("step 0: refreshing access token")
	freshAccessToken, _, err := api.RefreshToken(clientID, clientSecret, refreshToken)
	if err != nil {
		log.Fatal("RefreshToken:", err)
	}
	accessToken = freshAccessToken
	slog.Info("token refreshed")

	// Auth app
	slog.Info("step 1: authenticating app")
	if err := client.AuthApp(clientID, clientSecret); err != nil {
		log.Fatal("AuthApp:", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Discover account ID from the access token
	slog.Info("step 1b: listing accounts to discover correct account ID")
	accounts, err := client.GetAccountList(accessToken)
	if err != nil {
		log.Fatal("GetAccountList:", err)
	}
	if len(accounts) == 0 {
		log.Fatal("no accounts found for this access token")
	}
	for _, a := range accounts {
		slog.Info("found account", "ctidAccountID", a.CtidTraderAccountID, "login", a.TraderLogin, "isLive", a.IsLive, "broker", a.BrokerName)
	}
	// Pick demo or live account matching the requested mode
	var chosenAccountID int64
	for _, a := range accounts {
		if a.IsLive != demo { // demo==true means we want IsLive==false
			chosenAccountID = a.CtidTraderAccountID
			break
		}
	}
	if chosenAccountID == 0 {
		// fallback: just use the first account
		chosenAccountID = accounts[0].CtidTraderAccountID
	}
	slog.Info("using account", "ctidAccountID", chosenAccountID)
	client.SetAccountID(chosenAccountID)

	// Auth account
	slog.Info("step 2: authenticating account")
	if err := client.AuthAccount(accessToken); err != nil {
		log.Fatal("AuthAccount:", err)
	}

	// Subscribe spots to get current price
	slog.Info("step 3: subscribing to spots")
	if err := client.SubscribeSpots(); err != nil {
		log.Fatal("SubscribeSpots:", err)
	}

	slog.Info("step 4: waiting for first price tick (up to 10s)")
	time.Sleep(3 * time.Second)

	// Place the smallest possible BUY: 100,000 units, 50-pip SL, 100-pip TP
	const volume = int64(100_000)
	const slDist = 0.0050 // 50 pips
	const tpDist = 0.0100 // 100 pips

	slog.Info("step 5: placing BUY order", "volume", volume, "slDist", slDist, "tpDist", tpDist)
	if err := client.PlaceMarketOrder(api.TradeSideBuy, volume, slDist, tpDist, ""); err != nil {
		log.Fatal("PlaceMarketOrder:", err)
	}

	// Wait for the fill event to get position ID
	slog.Info("step 6: waiting for ORDER_FILLED event (up to 15s)")
	var positionID int64
	var filledVolume int64
	deadline := time.After(15 * time.Second)
waitFill:
	for {
		select {
		case ev := <-client.ExecutionCh:
			slog.Info("execution event",
				"type", ev.Type,
				"hasDeal", ev.HasDeal,
				"dealID", ev.Deal.DealID,
				"positionID", ev.Deal.PositionID,
				"filledVolume", ev.Deal.FilledVolume,
				"isClose", ev.Deal.IsClose,
				"errorCode", ev.ErrorCode,
			)
			if ev.Type == "ORDER_FILLED" && ev.HasDeal && !ev.Deal.IsClose {
				positionID = ev.Deal.PositionID
				filledVolume = ev.Deal.FilledVolume
				if filledVolume == 0 {
					filledVolume = volume
				}
				slog.Info("position opened",
					"positionID", positionID,
					"filledVolume", filledVolume,
				)
				break waitFill
			}
		case <-deadline:
			log.Fatal("timed out waiting for fill — no position opened, nothing to close")
		}
	}

	// Try close with STANDARD encoding: posID at field 3, volume at field 4
	slog.Info("step 7: trying STANDARD close (posID=field3, vol=field4)",
		"positionID", positionID, "volume", filledVolume)
	if err := client.ClosePosition(positionID, filledVolume); err != nil {
		log.Fatal("ClosePosition:", err)
	}

	slog.Info("step 8: waiting for close result (up to 10s)")
	deadline2 := time.After(10 * time.Second)
	standardWorked := false
	waitClose:
	for {
		select {
		case ev := <-client.ExecutionCh:
			slog.Info("close result event",
				"type", ev.Type,
				"hasDeal", ev.HasDeal,
				"isClose", ev.Deal.IsClose,
				"errorCode", ev.ErrorCode,
				"positionID", ev.Deal.PositionID,
			)
			switch {
			case ev.Type == "ORDER_FILLED" && ev.HasDeal && ev.Deal.IsClose:
				slog.Info("STANDARD CLOSE SUCCEEDED — field3=posID field4=vol is CORRECT")
				standardWorked = true
				break waitClose
			case ev.Type == "CLOSE_REJECTED":
				slog.Warn("STANDARD CLOSE REJECTED with INCORRECT_BOUNDARIES — trying variant")
				break waitClose
			case ev.Type == "ORDER_REJECTED":
				slog.Warn("ORDER_REJECTED", "errorCode", ev.ErrorCode)
				break waitClose
			}
		case <-deadline2:
			slog.Warn("timed out waiting for close result")
			break waitClose
		}
	}

	if standardWorked {
		slog.Info("CONCLUSION: standard proto encoding (field3=posID, field4=vol) works fine")
		slog.Info("root cause of INCORRECT_BOUNDARIES is NOT the field assignment")
		return
	}

	// Try VARIANT encoding: posID at field 4, volume at field 5
	slog.Info("step 9: trying VARIANT close (posID=field4, vol=field5)",
		"positionID", positionID, "volume", filledVolume)
	if err := client.ClosePositionVariant(positionID, filledVolume, 4, 5); err != nil {
		log.Fatal("ClosePositionVariant:", err)
	}

	slog.Info("step 10: waiting for variant close result (up to 10s)")
	deadline3 := time.After(10 * time.Second)
	for {
		select {
		case ev := <-client.ExecutionCh:
			slog.Info("variant close result",
				"type", ev.Type,
				"hasDeal", ev.HasDeal,
				"isClose", ev.Deal.IsClose,
				"errorCode", ev.ErrorCode,
			)
			switch {
			case ev.Type == "ORDER_FILLED" && ev.HasDeal && ev.Deal.IsClose:
				slog.Info("VARIANT CLOSE SUCCEEDED — CONFIRMED: broker expects posID=field4, vol=field5")
				slog.Info("ACTION REQUIRED: update encodeClosePositionReq in proto.go to use fields 4 and 5")
				return
			case ev.Type == "CLOSE_REJECTED":
				slog.Warn("VARIANT also rejected — field numbers are NOT the issue")
				slog.Warn("position still open — check cTrader UI, SL will close it eventually")
				return
			case ev.Type == "ORDER_REJECTED":
				slog.Warn("VARIANT ORDER_REJECTED", "errorCode", ev.ErrorCode)
				return
			}
		case <-deadline3:
			slog.Warn("timed out on variant close — position still open at broker, SL will handle it")
			return
		}
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("missing required env var %s", key)
	}
	return v
}

func mustEnvInt64(key string) int64 {
	var v int64
	_, err := fmt.Sscanf(os.Getenv(key), "%d", &v)
	if err != nil || v == 0 {
		log.Fatalf("missing or invalid env var %s", key)
	}
	return v
}
