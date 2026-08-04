package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type CTraderConfig struct {
	ClientID        string
	ClientSecret    string
	AccessToken     string
	RefreshToken    string
	AccountID       int64
	SymbolID        int64
	Demo            bool
	InitialBalance  float64
	OAuthRedirectURI string
	OAuthCallbackPort int
}

type BinanceConfig struct {
	APIKey    string
	APISecret string
	TestNet   bool
}

type Config struct {
	DatabaseURL string

	CTrader *CTraderConfig
	Binance *BinanceConfig

	EnableCTrader  bool
	CTraderSymbol  string
	EnableBinance  bool
	BinanceSymbol  string

	RiskPercent     float64
	MaxDailyLossPct float64
	EnableRiskMgmt  bool

	Period string

	DevMode bool

	SendTestPosition bool

	TelegramToken   string
	WebhookSecret   string
	WebhookPort     int

	Strategy    string
	MLModelDir  string
	MLOnnxLib   string // path to libonnxruntime.so; empty = use default search
}

func Load() (*Config, error) {
	godotenv.Load()

	var ctraderCfg *CTraderConfig
	if getEnv("ENABLE_CTRADER", "true") == "true" {
		accountID, err := strconv.ParseInt(mustEnv("CTRADER_ACCOUNT_ID"), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("CTRADER_ACCOUNT_ID must be a number: %w", err)
		}

		symbolID, err := strconv.ParseInt(getEnv("CTRADER_SYMBOL_ID", "1"), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("CTRADER_SYMBOL_ID must be a number: %w", err)
		}

		ctraderInitialBalance, err := strconv.ParseFloat(getEnv("CTRADER_INITIAL_BALANCE", "0"), 64)
		if err != nil {
			return nil, fmt.Errorf("CTRADER_INITIAL_BALANCE must be a number: %w", err)
		}

		oauthPort, _ := strconv.Atoi(getEnv("CTRADER_OAUTH_PORT", "8099"))
		ctraderCfg = &CTraderConfig{
			ClientID:          mustEnv("CTRADER_CLIENT_ID"),
			ClientSecret:      mustEnv("CTRADER_CLIENT_SECRET"),
			AccessToken:       getEnv("CTRADER_ACCESS_TOKEN", ""),
			RefreshToken:      getEnv("CTRADER_REFRESH_TOKEN", ""),
			AccountID:         accountID,
			SymbolID:          symbolID,
			Demo:              getEnv("CTRADER_DEMO", "true") == "true",
			InitialBalance:    ctraderInitialBalance,
			OAuthRedirectURI:  getEnv("CTRADER_OAUTH_REDIRECT_URI", "http://localhost:8099/callback"),
			OAuthCallbackPort: oauthPort,
		}
	}

	var binanceCfg *BinanceConfig
	if os.Getenv("BINANCE_API_KEY") != "" || os.Getenv("BINANCE_TESTNET_API_KEY") != "" {
		isTestNet := getEnv("BINANCE_TESTNET", "true") == "true"
		apiKey := getEnv("BINANCE_API_KEY", "")
		apiSecret := getEnv("BINANCE_API_SECRET", "")
		if isTestNet && os.Getenv("BINANCE_TESTNET_API_KEY") != "" {
			apiKey = getEnv("BINANCE_TESTNET_API_KEY", "")
			apiSecret = getEnv("BINANCE_TESTNET_API_SECRET", "")
		}
		binanceCfg = &BinanceConfig{
			APIKey:    apiKey,
			APISecret: apiSecret,
			TestNet:   isTestNet,
		}
	}

	riskPercent, err := strconv.ParseFloat(getEnv("RISK_PERCENT", "1.0"), 64)
	if err != nil {
		return nil, fmt.Errorf("RISK_PERCENT must be a number: %w", err)
	}

	maxDailyLossPct, err := strconv.ParseFloat(getEnv("MAX_DAILY_LOSS_PCT", "2.0"), 64)
	if err != nil {
		return nil, fmt.Errorf("MAX_DAILY_LOSS_PCT must be a number: %w", err)
	}

	webhookPort, err := strconv.Atoi(getEnv("WEBHOOK_PORT", "8090"))
	if err != nil {
		return nil, fmt.Errorf("WEBHOOK_PORT must be a number: %w", err)
	}

	cfg := &Config{
		DatabaseURL:     mustEnv("DATABASE_URL"),
		CTrader:         ctraderCfg,
		Binance:         binanceCfg,
		RiskPercent:     riskPercent,
		MaxDailyLossPct: maxDailyLossPct,
		EnableRiskMgmt:  getEnv("ENABLE_RISK_MGMT", "true") == "true",

		EnableCTrader: getEnv("ENABLE_CTRADER", "true") == "true",
		CTraderSymbol: getEnv("CTRADER_SYMBOL", "EURUSD"),
		EnableBinance: getEnv("ENABLE_BINANCE", "false") == "true",
		BinanceSymbol: getEnv("BINANCE_SYMBOL", "BTCUSDT"),

		Period: getEnv("TRADING_PERIOD", "M5"),

		DevMode:          getEnv("DEV_MODE", "false") == "true",
		SendTestPosition: getEnv("DEV_MODE", "false") == "true" && getEnv("SEND_TEST_POSITION", "false") == "true",

		TelegramToken: getEnv("TELEGRAM_BOT_TOKEN", ""),
		WebhookSecret: getEnv("WEBHOOK_SECRET", ""),
		WebhookPort:   webhookPort,

		Strategy:   getEnv("STRATEGY", "regime"),
		MLModelDir: getEnv("ML_MODEL_DIR", "ml"),
		MLOnnxLib:  getEnv("ML_ONNX_LIB", ""),
	}

	return cfg, nil
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required env var %s is not set", key))
	}
	return v
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

