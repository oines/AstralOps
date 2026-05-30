package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const cloudAuthStateTTL = 10 * time.Minute

type cloudAuthProvider string

const (
	cloudAuthProviderGoogle cloudAuthProvider = "google"
	cloudAuthProviderGitHub cloudAuthProvider = "github"
)

type cloudAuthState struct {
	BaseURL   string
	Provider  string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type cloudAuthStartRequest struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url,omitempty"`
}

type cloudAuthStartResponse struct {
	AuthURL     string `json:"auth_url"`
	CallbackURL string `json:"callback_url"`
	Provider    string `json:"provider"`
	ExpiresAt   string `json:"expires_at"`
}

type cloudAuthLogoutResponse = cloudMeshLogoutResult

func (a *app) handleCloudAuthAction(w http.ResponseWriter, r *http.Request) {
	route := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/cloud/auth/"), "/")
	switch route {
	case "start":
		a.handleCloudAuthStart(w, r)
	case "logout":
		a.handleCloudAuthLogout(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (a *app) handleCloudAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req cloudAuthStartRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	provider, err := normalizeCloudAuthProvider(req.Provider)
	if err != nil {
		writeActionError(w, err)
		return
	}
	baseURL, err := normalizeCloudAuthBaseURL(req.BaseURL, a.currentSettings().Cloud.BaseURL)
	if err != nil {
		writeActionError(w, err)
		return
	}
	state, err := randomCloudAuthState()
	if err != nil {
		writeActionError(w, newActionError(http.StatusInternalServerError, "cloud_auth_state_failed", err.Error()))
		return
	}
	callbackURL := fmt.Sprintf("http://127.0.0.1:%d/v1/cloud/auth/callback", a.runtimePort)
	expiresAt := time.Now().UTC().Add(cloudAuthStateTTL)
	a.rememberCloudAuthState(state, cloudAuthState{
		BaseURL:   baseURL,
		Provider:  string(provider),
		CreatedAt: time.Now().UTC(),
		ExpiresAt: expiresAt,
	})
	authURL, err := cloudAuthStartURL(baseURL, provider, callbackURL, state)
	if err != nil {
		a.forgetCloudAuthState(state)
		writeActionError(w, newActionError(http.StatusBadRequest, "cloud_auth_url_invalid", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, cloudAuthStartResponse{
		AuthURL:     authURL,
		CallbackURL: callbackURL,
		Provider:    string(provider),
		ExpiresAt:   expiresAt.Format(time.RFC3339),
	})
}

func (a *app) handleCloudAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	stateValue := strings.TrimSpace(r.URL.Query().Get("state"))
	state, ok := a.consumeCloudAuthState(stateValue)
	if !ok {
		writeCloudAuthCallbackHTML(w, "登录失败", "登录请求已过期或不匹配，请回到 AstralOps 重新登录。")
		return
	}
	if providerError := strings.TrimSpace(r.URL.Query().Get("error")); providerError != "" {
		writeCloudAuthCallbackHTML(w, "登录失败", "云账号登录被取消或失败："+providerError)
		return
	}
	loginCode := strings.TrimSpace(r.URL.Query().Get("login_code"))
	if loginCode == "" {
		writeCloudAuthCallbackHTML(w, "登录失败", "Cloud 没有返回 login code，请回到 AstralOps 重试。")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), cloudSyncTimeout)
	defer cancel()
	settings := a.currentSettings()
	exchanged, err := ExchangeCloudLoginCode(ctx, state.BaseURL, loginCode, a.store.hostInfo().Identity, settings.RemoteControl.Enabled, true, nil)
	if err != nil {
		writeCloudAuthCallbackHTML(w, "登录失败", "Cloud token exchange 失败："+err.Error())
		return
	}
	if strings.TrimSpace(exchanged.AccountToken) == "" {
		writeCloudAuthCallbackHTML(w, "登录失败", "Cloud 没有返回账号 token，请回到 AstralOps 重试。")
		return
	}
	if err := a.enableCloudAccount(state.BaseURL, exchanged.AccountToken); err != nil {
		writeCloudAuthCallbackHTML(w, "登录失败", "本机保存账号失败："+err.Error())
		return
	}
	title := "登录完成"
	detail := "AstralOps 已连接云账号，可以回到应用继续使用。"
	if exchanged.Account.AccountIDHash != "" {
		detail = "账号 " + exchanged.Account.AccountIDHash + " 已连接，可以回到应用继续使用。"
	}
	writeCloudAuthCallbackHTML(w, title, detail)
}

func (a *app) handleCloudAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	result, err := a.logoutCloudMesh(r.Context(), true)
	if err != nil {
		writeActionError(w, newActionError(http.StatusBadRequest, "cloud_logout_failed", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, cloudAuthLogoutResponse(result))
}

func (a *app) enableCloudAccount(baseURL, accountToken string) error {
	enabled := true
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	accountToken = strings.TrimSpace(accountToken)
	_, err := a.settings.patchWithHook(appSettingsPatch{Cloud: &cloudSettingsPatch{Enabled: &enabled, BaseURL: &baseURL, AccountToken: &accountToken}}, func(previous, next AppSettings) error {
		if cloudSettingsChanged(previous.Cloud, next.Cloud) {
			if err := a.applyCloudSettings(next.Cloud); err != nil {
				return err
			}
		}
		return nil
	}, func(previous AppSettings) {
		_ = a.applyCloudSettings(previous.Cloud)
	})
	if err == nil {
		a.enableRemoteControlForCloudLogin()
	}
	return err
}

func (a *app) enableRemoteControlForCloudLogin() {
	settings := a.currentSettings()
	if settings.RemoteControl.Enabled {
		return
	}
	enabled := true
	lanDiscovery := true
	_, err := a.settings.patchWithHook(appSettingsPatch{RemoteControl: &remoteControlSettingsPatch{Enabled: &enabled, LANDiscovery: &lanDiscovery}}, func(previous, next AppSettings) error {
		if remoteControlSettingsChanged(previous.RemoteControl, next.RemoteControl) {
			if err := a.applyRemoteControlSettings(next.RemoteControl); err != nil {
				return err
			}
			if err := a.writeRuntimeFile(); err != nil {
				return err
			}
			a.syncCloudRegistrationSoon(next)
		}
		return nil
	}, func(previous AppSettings) {
		_ = a.applyRemoteControlSettings(previous.RemoteControl)
		_ = a.writeRuntimeFile()
	})
	if err != nil {
		log.Printf("astralops remote control auto-enable after cloud login: %v", err)
	}
}

func normalizeCloudAuthProvider(value string) (cloudAuthProvider, error) {
	switch cloudAuthProvider(strings.ToLower(strings.TrimSpace(value))) {
	case cloudAuthProviderGoogle:
		return cloudAuthProviderGoogle, nil
	case cloudAuthProviderGitHub:
		return cloudAuthProviderGitHub, nil
	default:
		return "", newActionError(http.StatusBadRequest, "cloud_auth_provider_invalid", "cloud auth provider must be google or github")
	}
}

func normalizeCloudAuthBaseURL(value, fallback string) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(value), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(fallback), "/")
	}
	if baseURL == "" {
		return "", newActionError(http.StatusBadRequest, "cloud_base_url_required", "cloud base url required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", newActionError(http.StatusBadRequest, "cloud_base_url_invalid", "cloud base url invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", newActionError(http.StatusBadRequest, "cloud_base_url_invalid", "cloud base url scheme must be http or https")
	}
	return baseURL, nil
}

func cloudAuthStartURL(baseURL string, provider cloudAuthProvider, callbackURL, state string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/v1/auth/" + string(provider) + "/start")
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("redirect_uri", callbackURL)
	query.Set("state", state)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func randomCloudAuthState() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

func (a *app) rememberCloudAuthState(state string, value cloudAuthState) {
	a.cloudAuthMu.Lock()
	defer a.cloudAuthMu.Unlock()
	if a.cloudAuthStates == nil {
		a.cloudAuthStates = map[string]cloudAuthState{}
	}
	now := time.Now().UTC()
	for key, existing := range a.cloudAuthStates {
		if !now.Before(existing.ExpiresAt) {
			delete(a.cloudAuthStates, key)
		}
	}
	a.cloudAuthStates[state] = value
}

func (a *app) consumeCloudAuthState(state string) (cloudAuthState, bool) {
	a.cloudAuthMu.Lock()
	defer a.cloudAuthMu.Unlock()
	if a.cloudAuthStates == nil {
		return cloudAuthState{}, false
	}
	value, ok := a.cloudAuthStates[state]
	if ok {
		delete(a.cloudAuthStates, state)
	}
	if !ok || !time.Now().UTC().Before(value.ExpiresAt) {
		return cloudAuthState{}, false
	}
	return value, true
}

func (a *app) forgetCloudAuthState(state string) {
	a.cloudAuthMu.Lock()
	defer a.cloudAuthMu.Unlock()
	delete(a.cloudAuthStates, state)
}

func writeCloudAuthCallbackHTML(w http.ResponseWriter, title, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>%s</title>
  <style>
    body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #f5f5f7; color: #1d1d1f; }
    main { min-height: 100vh; display: grid; place-items: center; padding: 32px; box-sizing: border-box; }
    section { width: min(520px, 100%%); border: 1px solid rgba(0,0,0,.1); border-radius: 12px; background: white; padding: 28px; box-shadow: 0 20px 60px rgba(0,0,0,.08); }
    h1 { margin: 0 0 8px; font-size: 22px; line-height: 1.3; }
    p { margin: 0; color: #6e6e73; font-size: 14px; line-height: 1.6; }
  </style>
</head>
<body><main><section><h1>%s</h1><p>%s</p></section></main></body>
</html>`, html.EscapeString(title), html.EscapeString(title), html.EscapeString(detail))
}
