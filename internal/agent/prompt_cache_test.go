package agent

import (
	"strings"
	"testing"

	"metiq/internal/store/state"
)

func TestResolvePromptCacheProfile_OpenAICompatibleLlamaServer(t *testing.T) {
	profile, err := ResolvePromptCacheProfile(PromptCacheProviderOpenAICompatible, &state.ProviderPromptCacheConfig{
		Backend: "llama_server",
	})
	if err != nil {
		t.Fatalf("ResolvePromptCacheProfile: %v", err)
	}
	if !profile.Enabled {
		t.Fatal("expected profile to be enabled when backend is set")
	}
	if profile.Backend != PromptCacheBackendLlamaServer {
		t.Fatalf("expected llama_server backend, got %#v", profile)
	}
	if profile.DynamicContextPlacement != DynamicContextPlacementLateUser {
		t.Fatalf("expected late_user placement by default, got %#v", profile)
	}
	if !profile.SendLlamaCachePrompt {
		t.Fatalf("expected llama-server profile to request cache_prompt plumbing, got %#v", profile)
	}
}

func TestResolvePromptCacheProfile_OpenAICompatibleSystemPlacementSurvives(t *testing.T) {
	profile, err := ResolvePromptCacheProfile(PromptCacheProviderOpenAICompatible, &state.ProviderPromptCacheConfig{
		Backend:                 "vllm",
		DynamicContextPlacement: "system",
	})
	if err != nil {
		t.Fatalf("ResolvePromptCacheProfile: %v", err)
	}
	if !profile.Enabled || profile.Backend != PromptCacheBackendVLLM || profile.DynamicContextPlacement != DynamicContextPlacementSystem {
		t.Fatalf("expected explicit system placement to survive, got %#v", profile)
	}
}

func TestResolvePromptCacheProfile_OpenAICompatibleDisabledWins(t *testing.T) {
	profile, err := ResolvePromptCacheProfile(PromptCacheProviderOpenAICompatible, &state.ProviderPromptCacheConfig{
		Enabled: state.BoolPtr(false),
		Backend: "vllm",
	})
	if err != nil {
		t.Fatalf("ResolvePromptCacheProfile: %v", err)
	}
	if profile.Enabled || profile.Backend != PromptCacheBackendNone || profile.DynamicContextPlacement != DynamicContextPlacementSystem {
		t.Fatalf("expected disabled profile, got %#v", profile)
	}
}

func TestResolvePromptCacheProfile_NativeProviderDefaults(t *testing.T) {
	anthropic, err := ResolvePromptCacheProfile(PromptCacheProviderAnthropic, nil)
	if err != nil {
		t.Fatalf("anthropic default: %v", err)
	}
	if !anthropic.Enabled || !anthropic.UseAnthropicCacheControl || anthropic.DynamicContextPlacement != DynamicContextPlacementSystem {
		t.Fatalf("unexpected Anthropic default: %#v", anthropic)
	}

	gemini, err := ResolvePromptCacheProfile(PromptCacheProviderGemini, nil)
	if err != nil {
		t.Fatalf("gemini default: %v", err)
	}
	if !gemini.Enabled || !gemini.UseGeminiCachedContent || gemini.DynamicContextPlacement != DynamicContextPlacementSystem {
		t.Fatalf("unexpected Gemini default: %#v", gemini)
	}
}

func TestResolvePromptCacheProfile_InvalidCombinations(t *testing.T) {
	_, err := ResolvePromptCacheProfile(PromptCacheProviderAnthropic, &state.ProviderPromptCacheConfig{Backend: "vllm"})
	if err == nil || !strings.Contains(err.Error(), "OpenAI-compatible") {
		t.Fatalf("expected non-OpenAI backend error, got %v", err)
	}

	_, err = ResolvePromptCacheProfile(PromptCacheProviderGemini, &state.ProviderPromptCacheConfig{DynamicContextPlacement: "late_user"})
	if err == nil || !strings.Contains(err.Error(), "Gemini") {
		t.Fatalf("expected Gemini late_user error, got %v", err)
	}

	_, err = ResolvePromptCacheProfile(PromptCacheProviderOpenAICompatible, &state.ProviderPromptCacheConfig{Enabled: state.BoolPtr(true)})
	if err == nil || !strings.Contains(err.Error(), "backend is required") {
		t.Fatalf("expected OpenAI enabled-without-backend error, got %v", err)
	}

	_, err = ResolvePromptCacheProfile(PromptCacheProviderOpenAICompatible, &state.ProviderPromptCacheConfig{Backend: "bogus"})
	if err == nil || !strings.Contains(err.Error(), "unsupported prompt_cache.backend") {
		t.Fatalf("expected invalid backend error, got %v", err)
	}
}

func TestBuildProviderWithOverride_CarriesPromptCacheProfile(t *testing.T) {
	provider, err := BuildProviderWithOverride("custom-model", ProviderOverride{
		BaseURL: "http://localhost:8080/v1",
		PromptCache: &state.ProviderPromptCacheConfig{
			Backend: "vllm",
		},
	})
	if err != nil {
		t.Fatalf("BuildProviderWithOverride: %v", err)
	}
	openaiProvider, ok := provider.(*OpenAIChatProvider)
	if !ok {
		t.Fatalf("expected *OpenAIChatProvider, got %T", provider)
	}
	profile := openaiProvider.PromptCacheProfile()
	if !profile.Enabled || profile.Backend != PromptCacheBackendVLLM || profile.DynamicContextPlacement != DynamicContextPlacementLateUser {
		t.Fatalf("unexpected carried prompt-cache profile: %#v", profile)
	}
}
