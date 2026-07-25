package main

// control_rpc_models.go — control-RPC handlers for the models.* provider-auth
// surface (swarmstr-kmhu, BUCKET 1):
//
//	models.authStatus — per-provider credential/auth status (configured,
//	                    enabled, auth method: api_key | oauth | none)
//	models.authLogout — clear a provider's config-stored credentials
//	models.probe      — bounded reachability probe of a provider's endpoint
//
// Mirrors OpenClaw src/gateway/server-methods/models*.ts, mapped onto
// swarmstr's provider layer (state.ProvidersConfig + env credentials + the
// OAuth adapters in internal/agent). No secret material is returned — authStatus
// reports only booleans + the auth method, and probe derives its endpoint from
// operator config (never from request params), so it is not an SSRF primitive.

import (
	"context"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

// modelsProbeHTTPClient, when non-nil, overrides the probe HTTP client. Tests
// point this at an httptest server; production builds one per call with the
// requested timeout.
var modelsProbeHTTPClient *http.Client

// modelProviderEnv maps a provider id to its conventional API-key env var.
var modelProviderEnv = map[string]string{
	"openai":         "OPENAI_API_KEY",
	"anthropic":      "ANTHROPIC_API_KEY",
	"gemini":         "GEMINI_API_KEY",
	"xai":            "XAI_API_KEY",
	"cohere":         "COHERE_API_KEY",
	"groq":           "GROQ_API_KEY",
	"mistral":        "MISTRAL_API_KEY",
	"together":       "TOGETHER_API_KEY",
	"openrouter":     "OPENROUTER_API_KEY",
	"github-copilot": "GITHUB_COPILOT_TOKEN",
}

// modelProviderBaseURL maps a provider id to its default API base URL, used by
// models.probe when the provider config carries no explicit base_url.
var modelProviderBaseURL = map[string]string{
	"openai":         "https://api.openai.com",
	"anthropic":      "https://api.anthropic.com",
	"gemini":         "https://generativelanguage.googleapis.com",
	"xai":            "https://api.x.ai",
	"cohere":         "https://api.cohere.com",
	"groq":           "https://api.groq.com",
	"mistral":        "https://api.mistral.ai",
	"together":       "https://api.together.xyz",
	"openrouter":     "https://openrouter.ai",
	"github-copilot": "https://api.githubcopilot.com",
}

// knownModelProviders returns the sorted union of well-known providers and any
// providers present in config.
func knownModelProviders(cfg state.ConfigDoc) []string {
	set := map[string]struct{}{}
	for id := range modelProviderEnv {
		set[id] = struct{}{}
	}
	for id := range cfg.Providers {
		if id = strings.ToLower(strings.TrimSpace(id)); id != "" {
			set[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// providerAuthStatus reports credential/auth status for one provider without
// exposing any secret material.
func providerAuthStatus(cfg state.ConfigDoc, provider string) map[string]any {
	entry, hasEntry := cfg.Providers[provider]
	hasConfigKey := hasEntry && (strings.TrimSpace(entry.APIKey) != "" || len(entry.APIKeys) > 0)
	envVar := modelProviderEnv[provider]
	hasEnvKey := envVar != "" && strings.TrimSpace(os.Getenv(envVar)) != ""
	configured := hasConfigKey || hasEnvKey

	authMethod := "none"
	if configured {
		authMethod = "api_key"
	}
	// OAuth detection: Anthropic stores OAuth credentials in the api_key slot
	// ("sk-ant-oat…" or "access#refresh"); github-copilot auth is OAuth by
	// nature (device-flow token).
	if provider == "anthropic" && hasConfigKey {
		if _, _, isOAuth := agent.ParseAnthropicOAuthCredential(entry.APIKey); isOAuth {
			authMethod = "oauth"
		}
	}
	if provider == "github-copilot" && configured {
		authMethod = "oauth"
	}

	source := "none"
	switch {
	case hasConfigKey:
		source = "config"
	case hasEnvKey:
		source = "env"
	}

	rec := map[string]any{
		"provider":      provider,
		"configured":    configured,
		"authenticated": configured,
		"authMethod":    authMethod,
		"source":        source,
		"enabled":       hasEntry && entry.Enabled,
	}
	if hasEntry && strings.TrimSpace(entry.Model) != "" {
		rec["model"] = strings.TrimSpace(entry.Model)
	}
	return rec
}

func probeHTTPClient(timeout time.Duration) *http.Client {
	if modelsProbeHTTPClient != nil {
		return modelsProbeHTTPClient
	}
	return &http.Client{
		Timeout: timeout,
		// Do not follow redirects: a configured provider endpoint must not be
		// able to bounce the probe to loopback / link-local / metadata targets.
		// A redirect still counts as reachable (its 3xx status is reported).
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (h controlRPCHandler) handleModelsRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	switch method {
	case methods.MethodModelsAuthStatus:
		req, err := methods.DecodeModelsAuthStatusParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		var providers []string
		if req.Provider != "" {
			providers = []string{req.Provider}
		} else {
			providers = knownModelProviders(cfg)
		}
		out := make([]map[string]any, 0, len(providers))
		for _, p := range providers {
			out = append(out, providerAuthStatus(cfg, p))
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"providers": out, "count": len(out)}}, true, nil

	case methods.MethodModelsAuthLogout:
		req, err := methods.DecodeModelsAuthLogoutParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		entry, hasEntry := cfg.Providers[req.Provider]
		hadConfigCred := hasEntry && (strings.TrimSpace(entry.APIKey) != "" || len(entry.APIKeys) > 0)
		result := map[string]any{"ok": true, "provider": req.Provider, "cleared": hadConfigCred}
		if hadConfigCred {
			commit, err := commitRuntimeConfigMutation(ctx, h.deps.docsRepo, h.deps.configState, configMutationCommitRequest{
				BuildNext: func(current state.ConfigDoc) (state.ConfigDoc, error) {
					providers := make(state.ProvidersConfig, len(current.Providers))
					for id, pe := range current.Providers {
						providers[id] = pe
					}
					// Only clear when the provider still exists in committed state, so a
					// concurrent removal is not undone by re-inserting an empty entry.
					if cleared, ok := providers[req.Provider]; ok {
						cleared.APIKey = ""
						cleared.APIKeys = nil
						providers[req.Provider] = cleared
					}
					current.Providers = providers
					return current, nil
				},
			})
			if err != nil {
				return nostruntime.ControlRPCResult{}, true, err
			}
			result["hash"] = commit.Next.Hash()
			result["restart_pending"] = commit.RestartPending
		}
		// Env-var credentials cannot be cleared from a running process.
		envVar := modelProviderEnv[req.Provider]
		result["remaining_env"] = envVar != "" && strings.TrimSpace(os.Getenv(envVar)) != ""
		return nostruntime.ControlRPCResult{Result: result}, true, nil

	case methods.MethodModelsProbe:
		req, err := methods.DecodeModelsProbeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		entry, hasEntry := cfg.Providers[req.Provider]
		hasConfigKey := hasEntry && (strings.TrimSpace(entry.APIKey) != "" || len(entry.APIKeys) > 0)
		envVar := modelProviderEnv[req.Provider]
		configured := hasConfigKey || (envVar != "" && strings.TrimSpace(os.Getenv(envVar)) != "")

		base := ""
		if hasEntry && strings.TrimSpace(entry.BaseURL) != "" {
			base = strings.TrimSpace(entry.BaseURL)
		} else {
			base = modelProviderBaseURL[req.Provider]
		}
		result := map[string]any{
			"provider":   req.Provider,
			"configured": configured,
		}
		if req.Model != "" {
			result["model"] = req.Model
		}
		if base == "" {
			result["reachable"] = false
			result["reason"] = "no known endpoint for provider"
			return nostruntime.ControlRPCResult{Result: result}, true, nil
		}
		result["endpoint"] = base

		timeout := time.Duration(req.TimeoutMS) * time.Millisecond
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		httpReq, reqErr := http.NewRequestWithContext(probeCtx, http.MethodGet, base, nil)
		if reqErr != nil {
			result["reachable"] = false
			result["reason"] = reqErr.Error()
			return nostruntime.ControlRPCResult{Result: result}, true, nil
		}
		start := time.Now()
		resp, doErr := probeHTTPClient(timeout).Do(httpReq)
		latency := time.Since(start)
		result["latency_ms"] = latency.Milliseconds()
		if doErr != nil {
			// A response at any HTTP status proves reachability; a transport
			// error means unreachable.
			result["reachable"] = false
			result["reason"] = doErr.Error()
			return nostruntime.ControlRPCResult{Result: result}, true, nil
		}
		_ = resp.Body.Close()
		result["reachable"] = true
		result["status_code"] = resp.StatusCode
		return nostruntime.ControlRPCResult{Result: result}, true, nil

	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
}
