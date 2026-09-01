package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	TransportWebRTC            = "webrtc"
	TransportProviderWebSocket = "provider-websocket"

	DefaultOpenAIRealtimeModel     = "gpt-realtime-2.1"
	DefaultOpenAIRealtimeVoice     = "alloy"
	OpenAIRealtimeClientSecretsURL = "https://api.openai.com/v1/realtime/client_secrets"
	OpenAIRealtimeCallsURL         = "https://api.openai.com/v1/realtime/calls"
	OpenAIOfferResponseMaxBytes    = 256 * 1024
	openAISecretResponseMaxBytes   = 1 * 1024 * 1024
	openAISecretRequestTimeout     = 30 * time.Second
)

// SessionConfig is the provider-neutral request for a browser-owned realtime
// session. Providers must reject transports they do not implement.
type SessionConfig struct {
	Transport        string
	Voice            string
	Language         string
	Model            string
	SafetyIdentifier string
}

// Session is a provider response safe to return to a browser client. Standard
// fields use camelCase to match the gateway JSON surface; providers may add
// transport-specific fields.
type Session map[string]any

// SessionProvider is implemented by realtime providers that can mint a
// browser-owned session.
type SessionProvider interface {
	CreateBrowserSession(ctx context.Context, cfg SessionConfig) (Session, error)
}

// TransportProvider optionally advertises the browser transports a provider
// supports. An empty list means the provider did not publish transport metadata.
type TransportProvider interface {
	BrowserTransports() []string
}

// SupportsTransport reports whether advertised transport metadata includes the
// requested transport. The second return value is false when metadata is absent.
func SupportsTransport(provider any, transport string) (supported, advertised bool) {
	p, ok := provider.(TransportProvider)
	if !ok {
		return false, false
	}
	transports := p.BrowserTransports()
	if len(transports) == 0 {
		return false, false
	}
	transport = strings.TrimSpace(transport)
	for _, candidate := range transports {
		if strings.EqualFold(strings.TrimSpace(candidate), transport) {
			return true, true
		}
	}
	return false, true
}

// OpenAIRealtimeClient mints short-lived browser credentials using a standard
// server-side API key. Endpoint is injectable for deterministic tests; daemon
// callers should leave it empty so credentials can only reach api.openai.com.
type OpenAIRealtimeClient struct {
	HTTPClient *http.Client
	Endpoint   string
	OfferURL   string
}

// CreateSession requests an ephemeral OpenAI Realtime client secret. It never
// includes provider response bodies in errors, preventing credential leakage.
func (c *OpenAIRealtimeClient) CreateSession(ctx context.Context, apiKey string, cfg SessionConfig) (Session, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI Realtime API key is not configured")
	}
	if cfg.Transport != "" && cfg.Transport != TransportWebRTC {
		return nil, fmt.Errorf("OpenAI Realtime browser sessions do not support transport %q", cfg.Transport)
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = DefaultOpenAIRealtimeModel
	}
	voice := strings.TrimSpace(cfg.Voice)
	if voice == "" {
		voice = DefaultOpenAIRealtimeVoice
	}

	session := map[string]any{
		"type":  "realtime",
		"model": model,
		"audio": map[string]any{
			"output": map[string]any{"voice": voice},
		},
	}
	body, err := json.Marshal(map[string]any{"session": session})
	if err != nil {
		return nil, fmt.Errorf("encode OpenAI Realtime session request: %w", err)
	}

	requestCtx, cancel := context.WithTimeout(ctx, openAISecretRequestTimeout)
	defer cancel()
	endpoint := strings.TrimSpace(c.Endpoint)
	if endpoint == "" {
		endpoint = OpenAIRealtimeClientSecretsURL
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build OpenAI Realtime client secret request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	if safetyID := strings.TrimSpace(cfg.SafetyIdentifier); safetyID != "" {
		req.Header.Set("OpenAI-Safety-Identifier", safetyID)
	}

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request OpenAI Realtime client secret: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, openAISecretResponseMaxBytes))
		return nil, fmt.Errorf("OpenAI Realtime client secret request failed with status %d", resp.StatusCode)
	}

	payloadBytes, err := io.ReadAll(io.LimitReader(resp.Body, openAISecretResponseMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read OpenAI Realtime client secret response: %w", err)
	}
	if len(payloadBytes) > openAISecretResponseMaxBytes {
		return nil, fmt.Errorf("OpenAI Realtime client secret response exceeded %d bytes", openAISecretResponseMaxBytes)
	}
	var payload struct {
		Value        string `json:"value"`
		ClientSecret struct {
			Value string `json:"value"`
		} `json:"client_secret"`
		ExpiresAt int64 `json:"expires_at"`
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, fmt.Errorf("decode OpenAI Realtime client secret response")
	}
	secret := strings.TrimSpace(payload.Value)
	if secret == "" {
		secret = strings.TrimSpace(payload.ClientSecret.Value)
	}
	if secret == "" {
		return nil, fmt.Errorf("OpenAI Realtime client secret response did not include a value")
	}

	offerURL := strings.TrimSpace(c.OfferURL)
	if offerURL == "" {
		offerURL = OpenAIRealtimeCallsURL
	}
	result := Session{
		"transport":             TransportWebRTC,
		"clientSecret":          secret,
		"offerUrl":              offerURL,
		"offerResponseMaxBytes": OpenAIOfferResponseMaxBytes,
		"model":                 model,
		"voice":                 voice,
	}
	if payload.ExpiresAt > 0 && payload.ExpiresAt <= int64(^uint64(0)>>1)/int64(time.Second/time.Millisecond) {
		result["expiresAt"] = payload.ExpiresAt * int64(time.Second/time.Millisecond)
	}
	return result, nil
}
