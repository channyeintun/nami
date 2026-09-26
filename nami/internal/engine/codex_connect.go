package engine

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/api"
	commandspkg "github.com/channyeintun/nami/internal/commands"
	"github.com/channyeintun/nami/internal/config"
)

func connectCodex(cmd *slashCommandContext, methodInput string) (*connectResult, error) {
	method := strings.ToLower(strings.TrimSpace(methodInput))
	if method == "" {
		method = "browser"
	}
	if method != "browser" && method != "headless" && method != "env" && method != "manual" {
		return nil, emitTextResponse(cmd.bridge, "usage: /connect codex [browser|headless|env]")
	}

	codexAuth := config.LoadUser().Codex
	if method == "env" || method == "manual" {
		return connectCodexFromEnv(cmd)
	}

	appendSlashResponse(cmd.bridge, "Connecting Codex...\n\n")

	connectCtx, cancel := context.WithTimeout(cmd.ctx, 5*time.Minute)
	defer cancel()

	if strings.TrimSpace(codexAuth.RefreshToken) != "" {
		appendSlashResponse(cmd.bridge, "Refreshing saved Codex credentials...\n\n")
		tokens, refreshErr := api.RefreshCodexAccessToken(connectCtx, codexAuth.RefreshToken)
		if refreshErr == nil {
			applyCodexTokens(&codexAuth, tokens)
		} else {
			appendSlashResponse(cmd.bridge, "Saved Codex credentials could not be refreshed. Starting OAuth login...\n\n")
			clearCodexTokens(&codexAuth)
		}
	}

	if codexAccessTokenExpired(codexAuth, time.Now()) {
		appendSlashResponse(cmd.bridge, "Saved Codex access token expired. Starting OAuth login...\n\n")
		clearCodexTokens(&codexAuth)
	}

	if strings.TrimSpace(codexAuth.AccessToken) == "" {
		var (
			tokens api.CodexTokens
			err    error
		)
		if method == "headless" {
			tokens, err = runCodexHeadlessOAuth(connectCtx, cmd)
		} else {
			tokens, err = runCodexBrowserOAuth(connectCtx, cmd)
		}
		if err != nil {
			return nil, emitTextResponse(cmd.bridge, fmt.Sprintf("Codex connect failed: %v", err))
		}
		applyCodexTokens(&codexAuth, tokens)
	}

	// Saved onto the config as it is now, not as it was loaded before the
	// login, which can take minutes.
	if err := config.Update(func(cfg *config.Config) {
		cfg.Codex = codexAuth
		// Provider apart from model; see saveConnectedModel.
		cfg.Provider = "codex"
		cfg.Model = api.Presets["codex"].DefaultModel
	}); err != nil {
		return nil, emitTextResponse(cmd.bridge, fmt.Sprintf("save Codex credentials: %v", err))
	}

	return &connectResult{
		Provider: "codex",
		Model:    api.Presets["codex"].DefaultModel,
		// The client runs on what was just saved plus this process's
		// environment overrides, which the saved copy deliberately lacks.
		Config: config.Load(),
		FormatMessage: func(activeModelID string) string {
			return fmt.Sprintf("Codex connected. Set main model to %s.", activeModelID)
		},
	}, nil
}

func connectCodexFromEnv(cmd *slashCommandContext) (*connectResult, error) {
	statusCfg := config.LoadForWorkingDir(cmd.state.CWD)
	statusCfg.Model = cmd.state.ActiveModelID
	snapshot := commandspkg.DiscoverProviderSnapshot(statusCfg)
	status, _ := snapshot.LookupProvider("codex")
	if !status.Usable {
		spec, _ := commandspkg.LookupConnectProvider("codex")
		return nil, emitTextResponse(cmd.bridge, commandspkg.FormatConnectProviderGuidance(spec, snapshot))
	}
	if err := saveConnectedModel("codex", api.Presets["codex"].DefaultModel); err != nil {
		return nil, emitTextResponse(cmd.bridge, fmt.Sprintf("save Codex configuration: %v", err))
	}
	statusCfg.Provider = "codex"
	statusCfg.Model = api.Presets["codex"].DefaultModel
	return &connectResult{
		Provider: "codex",
		Model:    api.Presets["codex"].DefaultModel,
		Config:   statusCfg,
		FormatMessage: func(activeModelID string) string {
			return fmt.Sprintf("Codex is ready via %s. Set main model to %s.", status.AuthSource, activeModelID)
		},
	}, nil
}

func runCodexHeadlessOAuth(ctx context.Context, cmd *slashCommandContext) (api.CodexTokens, error) {
	device, err := api.StartCodexDeviceFlow(ctx)
	if err != nil {
		return api.CodexTokens{}, err
	}
	if err := commandspkg.OpenBrowserURL(device.VerificationURI); err == nil {
		appendSlashResponse(cmd.bridge, "Opened the browser automatically.\n")
	}
	appendSlashResponse(cmd.bridge, fmt.Sprintf("Visit: %s\nEnter code: %s\n\nWaiting for Codex authorization...\n\n", device.VerificationURI, device.UserCode))

	interval := time.Duration(device.IntervalSeconds)*time.Second + 3*time.Second
	if interval <= 0 {
		interval = 8 * time.Second
	}
	for {
		select {
		case <-ctx.Done():
			return api.CodexTokens{}, ctx.Err()
		case <-time.After(interval):
		}

		authorization, ok, err := api.PollCodexDeviceAuthorization(ctx, device.DeviceAuthID, device.UserCode)
		if err != nil {
			return api.CodexTokens{}, err
		}
		if !ok {
			continue
		}
		return api.ExchangeCodexDeviceAuthorization(ctx, authorization)
	}
}

func runCodexBrowserOAuth(ctx context.Context, cmd *slashCommandContext) (api.CodexTokens, error) {
	pkce, err := api.GenerateCodexPKCE()
	if err != nil {
		return api.CodexTokens{}, err
	}
	state, err := api.GenerateCodexState()
	if err != nil {
		return api.CodexTokens{}, err
	}
	redirectURI := fmt.Sprintf("http://localhost:%d/auth/callback", api.CodexOAuthPort)
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	server := &http.Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if oauthErr := strings.TrimSpace(query.Get("error")); oauthErr != "" {
			select {
			case errCh <- fmt.Errorf("%s", oauthErr):
			default:
			}
			http.Error(w, "Authorization failed", http.StatusBadRequest)
			return
		}
		if query.Get("state") != state {
			select {
			case errCh <- fmt.Errorf("invalid OAuth state"):
			default:
			}
			http.Error(w, "Invalid state", http.StatusBadRequest)
			return
		}
		code := strings.TrimSpace(query.Get("code"))
		if code == "" {
			select {
			case errCh <- fmt.Errorf("missing authorization code"):
			default:
			}
			http.Error(w, "Missing authorization code", http.StatusBadRequest)
			return
		}
		select {
		case codeCh <- code:
		default:
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><title>Nami Codex Authorization</title><p>Authorization successful. You can close this window.</p>"))
	})
	server.Handler = mux

	listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", api.CodexOAuthPort))
	if err != nil {
		return api.CodexTokens{}, err
	}
	defer server.Shutdown(context.Background())
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			select {
			case errCh <- serveErr:
			default:
			}
		}
	}()

	authorizeURL := api.BuildCodexAuthorizeURL(redirectURI, pkce, state)
	if err := commandspkg.OpenBrowserURL(authorizeURL); err == nil {
		appendSlashResponse(cmd.bridge, "Opened the browser automatically.\n")
	}
	appendSlashResponse(cmd.bridge, fmt.Sprintf("Visit: %s\n\nWaiting for Codex authorization...\n\n", authorizeURL))

	select {
	case <-ctx.Done():
		return api.CodexTokens{}, ctx.Err()
	case err := <-errCh:
		return api.CodexTokens{}, err
	case code := <-codeCh:
		return api.ExchangeCodexCodeForTokens(ctx, code, redirectURI, pkce)
	}
}

func applyCodexTokens(codexAuth *config.CodexAuth, tokens api.CodexTokens) {
	if codexAuth == nil {
		return
	}
	codexAuth.AccessToken = tokens.AccessToken
	if strings.TrimSpace(tokens.RefreshToken) != "" {
		codexAuth.RefreshToken = tokens.RefreshToken
	}
	if accountID := api.ExtractCodexAccountID(tokens); accountID != "" {
		codexAuth.AccountID = accountID
	}
	expiresIn := tokens.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	codexAuth.ExpiresAtUnixMS = time.Now().Add(time.Duration(expiresIn) * time.Second).UnixMilli()
}

func clearCodexTokens(codexAuth *config.CodexAuth) {
	if codexAuth == nil {
		return
	}
	codexAuth.AccessToken = ""
	codexAuth.RefreshToken = ""
	codexAuth.ExpiresAtUnixMS = 0
	codexAuth.AccountID = ""
}

func codexAccessTokenExpired(codexAuth config.CodexAuth, now time.Time) bool {
	if strings.TrimSpace(codexAuth.AccessToken) == "" {
		return false
	}
	if codexAuth.ExpiresAtUnixMS <= 0 {
		return false
	}
	return now.UnixMilli() >= codexAuth.ExpiresAtUnixMS
}
