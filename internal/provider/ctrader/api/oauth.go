package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

func InitiateOAuthFlow(clientID, clientSecret, redirectURI string, callbackPort int) (accessToken, refreshToken string, err error) {
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	srv := &http.Server{Addr: fmt.Sprintf(":%d", callbackPort), Handler: mux}

	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "No authorization code — open the authorization URL from the bot logs, not this URL directly.", http.StatusBadRequest)
			return
		}
		fmt.Fprintln(w, "Authorization successful — you can close this tab. The bot will continue.")
		codeCh <- code
	})

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("oauth callback server: %w", err)
		}
	}()
	defer srv.Shutdown(context.Background()) //nolint:errcheck

	authURL := fmt.Sprintf(
		"https://id.ctrader.com/my/settings/openapi/grantingaccess/?client_id=%s&redirect_uri=%s&scope=trading&product=web",
		clientID, redirectURI,
	)

	slog.Warn("=== cTrader OAuth required ===")
	slog.Warn("Open this URL in your browser to authorize the bot", "url", authURL)
	slog.Warn("Waiting up to 5 minutes for authorization...")

	select {
	case code := <-codeCh:
		accessToken, refreshToken, err = ExchangeCode(clientID, clientSecret, code, redirectURI)
		if err != nil {
			return "", "", fmt.Errorf("oauth exchange: %w", err)
		}
		slog.Info("OAuth authorization successful — tokens obtained")
		return accessToken, refreshToken, nil

	case err := <-errCh:
		return "", "", err

	case <-time.After(5 * time.Minute):
		return "", "", fmt.Errorf("oauth flow timed out — no authorization received within 5 minutes")
	}
}

func ExchangeCode(clientID, clientSecret, code, redirectURI string) (accessToken, refreshToken string, err error) {
	resp, err := http.Get(fmt.Sprintf(
		"https://openapi.ctrader.com/apps/token?grant_type=authorization_code&code=%s&redirect_uri=%s&client_id=%s&client_secret=%s",
		code, redirectURI, clientID, clientSecret,
	))
	if err != nil {
		return "", "", fmt.Errorf("ExchangeCode: %w", err)
	}
	defer resp.Body.Close()

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", "", fmt.Errorf("ExchangeCode decode: %w", err)
	}
	if code, ok := tr.ErrorCode.(string); ok && code != "" {
		return "", "", fmt.Errorf("ExchangeCode: %s: %v", code, tr.Description)
	}
	if tr.AccessToken == "" {
		return "", "", fmt.Errorf("ExchangeCode: empty access token in response")
	}
	return tr.AccessToken, tr.RefreshToken, nil
}
