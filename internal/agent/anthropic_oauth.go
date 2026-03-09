// Package agent — Anthropic OAuth credential support.
//
// OpenClaw stores Anthropic OAuth credentials in the format:
//
//	access_token#refresh_token
//
// Standard Anthropic OAuth access tokens start with "sk-ant-oat".
// Both formats are accepted; plain API keys ("sk-ant-api") are passed through
// as-is and are not treated as OAuth.
//
// OAuth-authenticated requests use:
//   - Authorization: Bearer <access_token>  (not x-api-key)
//   - anthropic-beta: claude-code-20250219,oauth-2025-04-20
//   - user-agent: claude-cli/2.1.62
//   - x-app: cli
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// anthropicOAuthBeta lists the beta features required for OAuth-authenticated requests.
	anthropicOAuthBeta = "claude-code-20250219,oauth-2025-04-20,fine-grained-tool-streaming-2025-05-14"

	// anthropicOAuthUserAgent mimics the Claude CLI user-agent.
	anthropicOAuthUserAgent = "claude-cli/2.1.62"

	// anthropicOAuthTokenURL is Anthropic's OAuth token endpoint.
	anthropicOAuthTokenURL = "https://auth.anthropic.com/oauth/token"

	// anthropicOAuthClientID is the public Claude CLI OAuth client ID.
	// This ID is embedded in the Claude CLI binary and is safe to use here.
	anthropicOAuthClientID = "9d1c250a-e61b-48f6-9651-b0f7c4f6e941"
)

// ParseAnthropicOAuthCredential parses a credential string into its component parts.
//
// Accepted formats:
//   - "sk-ant-api03-..."            → plain API key, not OAuth (returns isOAuth=false)
//   - "sk-ant-oat01-..."            → direct OAuth access token
//   - "access_token#refresh_token"  → OpenClaw stored credential
func ParseAnthropicOAuthCredential(cred string) (access, refresh string, isOAuth bool) {
	cred = strings.TrimSpace(cred)
	if cred == "" {
		return "", "", false
	}
	// Plain API keys (sk-ant-api...) are not OAuth.
	if strings.HasPrefix(cred, "sk-ant-api") {
		return "", "", false
	}
	// Direct OAuth access token.
	if strings.HasPrefix(cred, "sk-ant-oat") {
		return cred, "", true
	}
	// OpenClaw format: "access_token#refresh_token"
	if idx := strings.Index(cred, "#"); idx != -1 {
		a := strings.TrimSpace(cred[:idx])
		r := strings.TrimSpace(cred[idx+1:])
		if a != "" {
			return a, r, true
		}
	}
	return "", "", false
}

// oauthTokenCache caches refreshed OAuth tokens in-process to avoid redundant
// refresh calls across concurrent requests.
var oauthTokenCache struct {
	mu      sync.Mutex
	access  string
	refresh string
	expiry  time.Time
}

// AnthropicOAuthRefresh exchanges a refresh token for a new access token.
// Returns the new access and refresh tokens.
func AnthropicOAuthRefresh(ctx context.Context, refreshToken string) (newAccess, newRefresh string, err error) {
	if strings.TrimSpace(refreshToken) == "" {
		return "", "", fmt.Errorf("anthropic oauth: no refresh token available")
	}

	body := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {anthropicOAuthClientID},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicOAuthTokenURL,
		strings.NewReader(body.Encode()))
	if err != nil {
		return "", "", fmt.Errorf("anthropic oauth refresh: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("anthropic oauth refresh: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if jsonErr := json.Unmarshal(raw, &result); jsonErr != nil {
		return "", "", fmt.Errorf("anthropic oauth refresh: decode response: %w (body: %s)", jsonErr, string(raw))
	}
	if errMsg, ok := result["error"].(string); ok {
		desc, _ := result["error_description"].(string)
		return "", "", fmt.Errorf("anthropic oauth refresh failed: %s: %s", errMsg, desc)
	}
	access, _ := result["access_token"].(string)
	if access = strings.TrimSpace(access); access == "" {
		return "", "", fmt.Errorf("anthropic oauth refresh: no access_token in response (body: %s)", string(raw))
	}
	newRefresh, _ = result["refresh_token"].(string)
	if newRefresh == "" {
		newRefresh = refreshToken // keep old refresh if not rotated
	}
	return access, newRefresh, nil
}

// applyAnthropicOAuthHeaders sets the headers required for OAuth-authenticated
// Anthropic API calls on req.
func applyAnthropicOAuthHeaders(req *http.Request, accessToken string) {
	req.Header.Del("x-api-key")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", anthropicOAuthBeta)
	req.Header.Set("user-agent", anthropicOAuthUserAgent)
	req.Header.Set("x-app", "cli")
}
