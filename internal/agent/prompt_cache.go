package agent

import (
	"fmt"
	"strings"

	"metiq/internal/store/state"
)

// PromptCacheBackend names provider/backend-specific prompt-cache behavior.
type PromptCacheBackend string

const (
	PromptCacheBackendNone        PromptCacheBackend = ""
	PromptCacheBackendLlamaServer PromptCacheBackend = "llama_server"
	PromptCacheBackendVLLM        PromptCacheBackend = "vllm"
)

// DynamicContextPlacement controls where per-turn dynamic context is emitted.
type DynamicContextPlacement string

const (
	DynamicContextPlacementSystem   DynamicContextPlacement = "system"
	DynamicContextPlacementLateUser DynamicContextPlacement = "late_user"
)

// PromptCacheProviderClass identifies the provider family whose cache policy is
// being resolved. It is intentionally runtime-only; persisted config stays in
// state.ProviderPromptCacheConfig.
type PromptCacheProviderClass string

const (
	PromptCacheProviderAnthropic        PromptCacheProviderClass = "anthropic"
	PromptCacheProviderGemini           PromptCacheProviderClass = "gemini"
	PromptCacheProviderOpenAICompatible PromptCacheProviderClass = "openai_compatible"
	PromptCacheProviderUnsupported      PromptCacheProviderClass = "unsupported"
)

// PromptCacheProfile is the runtime policy derived from provider config.
// Providers use it to select prompt layout, native cache toggles, and any
// backend-specific OpenAI-compatible request fields.
type PromptCacheProfile struct {
	Enabled bool

	// Native provider caches.
	UseAnthropicCacheControl bool
	UseGeminiCachedContent   bool

	// OpenAI-compatible prefix caches.
	Backend                 PromptCacheBackend
	DynamicContextPlacement DynamicContextPlacement
	SendLlamaCachePrompt    bool
}

type promptCacheProfileProvider interface {
	PromptCacheProfile() PromptCacheProfile
}

// ResolvePromptCacheProfile validates a provider-level prompt_cache block and
// resolves it into the runtime policy for the given provider class.
func ResolvePromptCacheProfile(providerClass PromptCacheProviderClass, cfg *state.ProviderPromptCacheConfig) (PromptCacheProfile, error) {
	profile := defaultPromptCacheProfile(providerClass)
	if cfg == nil {
		return profile, nil
	}

	backend, err := normalizePromptCacheBackend(cfg.Backend)
	if err != nil {
		return PromptCacheProfile{}, err
	}
	placement, err := normalizeDynamicContextPlacement(cfg.DynamicContextPlacement)
	if err != nil {
		return PromptCacheProfile{}, err
	}

	enabled := profile.Enabled
	if cfg.Enabled != nil {
		enabled = *cfg.Enabled
	}

	switch providerClass {
	case PromptCacheProviderAnthropic:
		if backend != PromptCacheBackendNone {
			return PromptCacheProfile{}, fmt.Errorf("prompt_cache.backend=%q is only supported for OpenAI-compatible providers", backend)
		}
		if placement == DynamicContextPlacementLateUser {
			return PromptCacheProfile{}, fmt.Errorf("prompt_cache.dynamic_context_placement=%q is not supported for Anthropic providers", placement)
		}
		profile.Enabled = enabled
		profile.UseAnthropicCacheControl = enabled
		profile.UseGeminiCachedContent = false
		profile.Backend = PromptCacheBackendNone
		profile.DynamicContextPlacement = DynamicContextPlacementSystem
		profile.SendLlamaCachePrompt = false
		return profile, nil

	case PromptCacheProviderGemini:
		if backend != PromptCacheBackendNone {
			return PromptCacheProfile{}, fmt.Errorf("prompt_cache.backend=%q is only supported for OpenAI-compatible providers", backend)
		}
		if placement == DynamicContextPlacementLateUser {
			return PromptCacheProfile{}, fmt.Errorf("prompt_cache.dynamic_context_placement=%q is not supported for Gemini providers", placement)
		}
		profile.Enabled = enabled
		profile.UseAnthropicCacheControl = false
		profile.UseGeminiCachedContent = enabled
		profile.Backend = PromptCacheBackendNone
		profile.DynamicContextPlacement = DynamicContextPlacementSystem
		profile.SendLlamaCachePrompt = false
		return profile, nil

	case PromptCacheProviderOpenAICompatible:
		if cfg.Enabled != nil && *cfg.Enabled && backend == PromptCacheBackendNone {
			return PromptCacheProfile{}, fmt.Errorf("prompt_cache.backend is required when prompt_cache.enabled=true for OpenAI-compatible providers")
		}
		if backend != PromptCacheBackendNone && cfg.Enabled == nil {
			enabled = true
		}
		if !enabled {
			return disabledPromptCacheProfile(), nil
		}
		if backend == PromptCacheBackendNone {
			return disabledPromptCacheProfile(), nil
		}
		if placement == "" {
			placement = DynamicContextPlacementLateUser
		}
		profile.Enabled = true
		profile.UseAnthropicCacheControl = false
		profile.UseGeminiCachedContent = false
		profile.Backend = backend
		profile.DynamicContextPlacement = placement
		profile.SendLlamaCachePrompt = backend == PromptCacheBackendLlamaServer
		return profile, nil

	default:
		if backend != PromptCacheBackendNone || placement == DynamicContextPlacementLateUser || enabled {
			return PromptCacheProfile{}, fmt.Errorf("prompt_cache is not supported for this provider")
		}
		return disabledPromptCacheProfile(), nil
	}
}

func defaultPromptCacheProfile(providerClass PromptCacheProviderClass) PromptCacheProfile {
	switch providerClass {
	case PromptCacheProviderAnthropic:
		return PromptCacheProfile{Enabled: true, UseAnthropicCacheControl: true, DynamicContextPlacement: DynamicContextPlacementSystem}
	case PromptCacheProviderGemini:
		return PromptCacheProfile{Enabled: true, UseGeminiCachedContent: true, DynamicContextPlacement: DynamicContextPlacementSystem}
	default:
		return disabledPromptCacheProfile()
	}
}

func disabledPromptCacheProfile() PromptCacheProfile {
	return PromptCacheProfile{DynamicContextPlacement: DynamicContextPlacementSystem}
}

func promptCacheProfileOrDefault(profile *PromptCacheProfile, providerClass PromptCacheProviderClass) PromptCacheProfile {
	if profile != nil {
		return *profile
	}
	return defaultPromptCacheProfile(providerClass)
}

func promptCacheProfilePtr(profile PromptCacheProfile) *PromptCacheProfile {
	return &profile
}

func normalizePromptCacheBackend(raw string) (PromptCacheBackend, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return PromptCacheBackendNone, nil
	case "llama_server", "llama-server", "llamaserver":
		return PromptCacheBackendLlamaServer, nil
	case "vllm":
		return PromptCacheBackendVLLM, nil
	default:
		return PromptCacheBackendNone, fmt.Errorf("unsupported prompt_cache.backend %q", raw)
	}
}

func normalizeDynamicContextPlacement(raw string) (DynamicContextPlacement, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "", nil
	case "system":
		return DynamicContextPlacementSystem, nil
	case "late_user", "late-user":
		return DynamicContextPlacementLateUser, nil
	default:
		return "", fmt.Errorf("unsupported prompt_cache.dynamic_context_placement %q", raw)
	}
}
