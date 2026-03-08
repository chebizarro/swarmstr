// This file implements OAuth-based provider authentication adapters.
// Currently supported: GitHub Copilot device-flow OAuth.
//
// Usage:
//
//	token, err := agent.GitHubCopilotAuth(ctx)
//	if err != nil { ... }
//	rt, err := agent.BuildRuntimeWithOverride("gpt-4o", agent.ProviderOverride{APIKey: token}, tools)
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// ── GitHub Copilot OAuth ─────────────────────────────────────────────────────

const (
	// ghCopilotClientID is the well-known public OAuth client ID for GitHub
	// Copilot.  This is the same ID used by VS Code's Copilot extension and is
	// safe to embed (it is public).
	ghCopilotClientID = "Iv1.b507a08c87ecfe98"

	ghDeviceAuthURL = "https://github.com/login/device/code"
	ghTokenURL      = "https://github.com/login/oauth/access_token"
	ghCopilotAPIURL = "https://api.githubcopilot.com"
	ghCopilotTokenCacheEnv = "GITHUB_COPILOT_TOKEN"
)

// ghCopilotTokenCache is a simple in-process cache so repeated BuildRuntime
// calls within the same process don't each trigger a full OAuth flow.
var ghCopilotTokenCache struct {
	mu      sync.Mutex
	token   string
	expires time.Time
}

// GitHubCopilotToken returns a valid Copilot API token.
// It checks (in order):
//  1. GITHUB_COPILOT_TOKEN env var
//  2. In-process cache
//  3. ~/.config/github-copilot/hosts.json (VS Code / CLI cache)
//  4. Device-flow OAuth (interactive — prints to stderr, reads from stdin via prompt)
//
// The prompt func, when non-nil, is called with the user-code and verification
// URL so callers can display or handle them.  Passing nil uses a default stderr
// printer.
func GitHubCopilotToken(ctx context.Context, prompt func(userCode, verifyURL string)) (string, error) {
	// 1. Env var.
	if t := strings.TrimSpace(os.Getenv(ghCopilotTokenCacheEnv)); t != "" {
		return t, nil
	}
	// 2. In-process cache.
	ghCopilotTokenCache.mu.Lock()
	if ghCopilotTokenCache.token != "" && time.Now().Before(ghCopilotTokenCache.expires) {
		tok := ghCopilotTokenCache.token
		ghCopilotTokenCache.mu.Unlock()
		return tok, nil
	}
	ghCopilotTokenCache.mu.Unlock()

	// 3. VS Code / GitHub CLI cache at ~/.config/github-copilot/hosts.json.
	if tok, ok := loadCopilotHostsToken(); ok {
		setCopilotCache(tok, time.Now().Add(50*time.Minute))
		return tok, nil
	}

	// 4. Device-flow OAuth.
	tok, err := ghCopilotDeviceFlow(ctx, prompt)
	if err != nil {
		return "", err
	}
	setCopilotCache(tok, time.Now().Add(50*time.Minute))
	return tok, nil
}

func setCopilotCache(token string, expires time.Time) {
	ghCopilotTokenCache.mu.Lock()
	ghCopilotTokenCache.token = token
	ghCopilotTokenCache.expires = expires
	ghCopilotTokenCache.mu.Unlock()
}

// ghCopilotDeviceFlow initiates the GitHub device authorization flow.
func ghCopilotDeviceFlow(ctx context.Context, prompt func(userCode, verifyURL string)) (string, error) {
	// Step 1: request device code.
	deviceResp, err := ghRequestDeviceCode(ctx)
	if err != nil {
		return "", fmt.Errorf("github copilot device flow: %w", err)
	}
	if prompt != nil {
		prompt(deviceResp.UserCode, deviceResp.VerificationURI)
	} else {
		fmt.Fprintf(os.Stderr, "\nGitHub Copilot authorization required.\n")
		fmt.Fprintf(os.Stderr, "  1. Open: %s\n", deviceResp.VerificationURI)
		fmt.Fprintf(os.Stderr, "  2. Enter code: %s\n\n", deviceResp.UserCode)
	}

	// Step 2: poll for token.
	interval := time.Duration(deviceResp.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(deviceResp.ExpiresIn) * time.Second)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
		tok, pending, err := ghPollToken(ctx, deviceResp.DeviceCode)
		if err != nil {
			return "", fmt.Errorf("github copilot poll token: %w", err)
		}
		if pending {
			continue
		}
		return tok, nil
	}
	return "", fmt.Errorf("github copilot device flow: authorization timed out")
}

type ghDeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

func ghRequestDeviceCode(ctx context.Context) (ghDeviceCodeResponse, error) {
	body := url.Values{
		"client_id": {ghCopilotClientID},
		"scope":     {"copilot"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ghDeviceAuthURL, strings.NewReader(body.Encode()))
	if err != nil {
		return ghDeviceCodeResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ghDeviceCodeResponse{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out ghDeviceCodeResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return ghDeviceCodeResponse{}, fmt.Errorf("decode device code response: %w", err)
	}
	return out, nil
}

func ghPollToken(ctx context.Context, deviceCode string) (token string, pending bool, err error) {
	body := url.Values{
		"client_id":   {ghCopilotClientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, ghTokenURL, strings.NewReader(body.Encode()))
	if reqErr != nil {
		return "", false, reqErr
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, doErr := http.DefaultClient.Do(req)
	if doErr != nil {
		return "", false, doErr
	}
	defer resp.Body.Close()
	var result map[string]any
	if decErr := json.NewDecoder(resp.Body).Decode(&result); decErr != nil {
		return "", false, decErr
	}
	if errCode, ok := result["error"].(string); ok {
		if errCode == "authorization_pending" || errCode == "slow_down" {
			return "", true, nil
		}
		return "", false, fmt.Errorf("oauth error: %s", errCode)
	}
	tok, ok := result["access_token"].(string)
	if !ok || strings.TrimSpace(tok) == "" {
		return "", false, fmt.Errorf("missing access_token in response")
	}
	return tok, false, nil
}

// loadCopilotHostsToken tries to read an existing GitHub Copilot token from
// the VS Code / GitHub CLI cache (~/.config/github-copilot/hosts.json).
func loadCopilotHostsToken() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	paths := []string{
		home + "/.config/github-copilot/hosts.json",
		home + "/Library/Application Support/github-copilot/hosts.json", // macOS
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		// hosts.json is {"github.com": {"oauth_token": "gho_..."}}
		var hosts map[string]map[string]any
		if json.Unmarshal(raw, &hosts) != nil {
			continue
		}
		for _, entry := range hosts {
			if tok, ok := entry["oauth_token"].(string); ok && tok != "" {
				return tok, true
			}
		}
	}
	return "", false
}

// ── ProviderAuthRegistry ─────────────────────────────────────────────────────
// Allows callers to register custom OAuth token fetchers by provider name.

// OAuthTokenFetcher returns a bearer token for the given provider.
type OAuthTokenFetcher func(ctx context.Context) (token string, err error)

var (
	oauthFetchersMu sync.RWMutex
	oauthFetchers   = map[string]OAuthTokenFetcher{}
)

// RegisterOAuthFetcher registers an OAuth token fetcher for provider providerType.
// Registrations happen at init() time (before the daemon starts).
func RegisterOAuthFetcher(providerType string, f OAuthTokenFetcher) {
	providerType = strings.ToLower(strings.TrimSpace(providerType))
	oauthFetchersMu.Lock()
	oauthFetchers[providerType] = f
	oauthFetchersMu.Unlock()
}

// FetchOAuthToken fetches a bearer token for the given provider type, if a
// fetcher has been registered.  Returns ("", false, nil) when no fetcher exists.
func FetchOAuthToken(ctx context.Context, providerType string) (token string, found bool, err error) {
	oauthFetchersMu.RLock()
	f, ok := oauthFetchers[strings.ToLower(strings.TrimSpace(providerType))]
	oauthFetchersMu.RUnlock()
	if !ok {
		return "", false, nil
	}
	tok, err := f(ctx)
	return tok, true, err
}

func init() {
	// Register GitHub Copilot as a built-in OAuth provider.
	RegisterOAuthFetcher("github-copilot", func(ctx context.Context) (string, error) {
		return GitHubCopilotToken(ctx, nil)
	})
}
