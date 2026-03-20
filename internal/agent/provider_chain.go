package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
)

// ─── Provider-level FallbackChain and ModelRouter wrappers ────────────────────
//
// These allow main.go to build a Provider that internally uses a FallbackChain
// and/or ModelRouter, while presenting the standard Provider.Generate() API.

// BuildChatProviderForModel creates a ChatProvider for the given model string
// and optional credentials. This is the bridge between model names and the
// ChatProvider interface used by the agentic loop and FallbackChain.
func BuildChatProviderForModel(model string, apiKey string, baseURL string) (ChatProvider, error) {
	norm := strings.ToLower(strings.TrimSpace(model))

	switch {
	case norm == "anthropic" || strings.HasPrefix(norm, "claude-"):
		if apiKey == "" {
			apiKey = strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
		}
		if apiKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY required for model %q", model)
		}
		return NewAnthropicChatProvider(apiKey), nil

	case norm == "gemini" || strings.HasPrefix(norm, "gemini-"):
		if apiKey == "" {
			for _, k := range []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY"} {
				if v := strings.TrimSpace(os.Getenv(k)); v != "" {
					apiKey = v
					break
				}
			}
		}
		return &GeminiChatProvider{APIKey: apiKey, Model: model}, nil

	case norm == "copilot-cli" || strings.HasPrefix(norm, "copilot-cli/"):
		cliModel := "gpt-4.1"
		if strings.HasPrefix(norm, "copilot-cli/") {
			cliModel = strings.TrimPrefix(norm, "copilot-cli/")
		}
		return &CopilotCLIChatProvider{Model: cliModel, CLIURL: baseURL}, nil

	case norm == "openai" || strings.HasPrefix(norm, "gpt-") ||
		strings.HasPrefix(norm, "o1-") || strings.HasPrefix(norm, "o3-") || strings.HasPrefix(norm, "o4-"):
		if apiKey == "" {
			apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
		}
		effectiveBase := baseURL
		if effectiveBase == "" {
			effectiveBase = "https://api.openai.com/v1"
		}
		return &OpenAIChatProviderChat{BaseURL: effectiveBase, APIKey: apiKey, Model: model}, nil
	}

	// Try OpenAI-compatible providers by prefix/alias.
	if compatBase, compatEnvKey := resolveOpenAICompat(norm); compatBase != "" {
		if apiKey == "" && compatEnvKey != "" {
			apiKey = strings.TrimSpace(os.Getenv(compatEnvKey))
		}
		effectiveBase := baseURL
		if effectiveBase == "" {
			effectiveBase = compatBase
		}
		return &OpenAIChatProviderChat{BaseURL: effectiveBase, APIKey: apiKey, Model: model}, nil
	}

	return nil, fmt.Errorf("cannot create ChatProvider for model %q", model)
}

// FallbackChainProvider implements Provider using a FallbackChain of ChatProviders.
// It drives the agentic loop with the FallbackChain as the underlying ChatProvider,
// giving automatic failover between models/providers on each API call.
type FallbackChainProvider struct {
	chain        *FallbackChain
	systemPrompt string
	logPrefix    string
}

// NewFallbackChainProvider creates a Provider that uses a FallbackChain internally.
// primaryModel and fallbackModels are used to build ChatProviders.
// The returned Provider can be used with NewProviderRuntime.
func NewFallbackChainProvider(
	primaryModel string,
	primaryAPIKey string,
	primaryBaseURL string,
	fallbackModels []string,
	overrides map[string]ProviderOverride,
	systemPrompt string,
) (*FallbackChainProvider, error) {
	// Build primary candidate.
	primaryCP, err := BuildChatProviderForModel(primaryModel, primaryAPIKey, primaryBaseURL)
	if err != nil {
		return nil, fmt.Errorf("primary model %q: %w", primaryModel, err)
	}

	candidates := []FallbackCandidate{
		{Name: primaryModel, Model: primaryModel, Provider: primaryCP},
	}

	// Build fallback candidates.
	for _, fbModel := range fallbackModels {
		fbModel = strings.TrimSpace(fbModel)
		if fbModel == "" {
			continue
		}
		var fbKey, fbBase string
		if ov, ok := overrides[fbModel]; ok {
			fbKey = ov.APIKey
			fbBase = ov.BaseURL
		}
		fbCP, fbErr := BuildChatProviderForModel(fbModel, fbKey, fbBase)
		if fbErr != nil {
			log.Printf("fallback-chain: skipping model %q: %v", fbModel, fbErr)
			continue
		}
		candidates = append(candidates, FallbackCandidate{
			Name: fbModel, Model: fbModel, Provider: fbCP,
		})
	}

	chain := NewFallbackChain(candidates, NewCooldownTracker())

	return &FallbackChainProvider{
		chain:        chain,
		systemPrompt: systemPrompt,
		logPrefix:    "fallback-chain",
	}, nil
}

// Generate implements Provider.
func (p *FallbackChainProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	return generateWithAgenticLoop(ctx, p.chain, turn, p.systemPrompt, p.logPrefix)
}

// RoutedProvider wraps a primary Provider and a light-model Provider.
// It uses a ModelRouter to select between them based on message complexity.
type RoutedProvider struct {
	primary      Provider
	light        Provider
	router       *ModelRouter
	primaryModel string
}

// NewRoutedProvider creates a Provider that routes between primary and light models.
// If lightModel is empty or cannot be resolved, routing is disabled and the
// primary provider is used for all requests.
func NewRoutedProvider(primary Provider, primaryModel string, lightModel string, threshold float64, tools ToolExecutor) *RoutedProvider {
	rp := &RoutedProvider{
		primary:      primary,
		primaryModel: primaryModel,
	}

	if lightModel == "" {
		return rp
	}

	router := NewModelRouter(lightModel, threshold)
	rp.router = router

	// Build the light provider.
	lightProv, err := NewProviderForModel(lightModel)
	if err != nil {
		log.Printf("routing: light model %q unavailable: %v — routing disabled", lightModel, err)
		return rp
	}
	rp.light = lightProv
	return rp
}

// Generate implements Provider.
func (p *RoutedProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	if p.router == nil || p.light == nil {
		return p.primary.Generate(ctx, turn)
	}

	// Pass history for better routing accuracy.
	hist := make([]LLMMessage, 0, len(turn.History))
	for _, h := range turn.History {
		hist = append(hist, LLMMessage{Role: h.Role, Content: h.Content, ToolCallID: h.ToolCallID})
	}

	model, usedLight, _ := p.router.SelectModel(turn.UserText, p.primaryModel, hist)
	_ = model // logged by router

	if usedLight {
		return p.light.Generate(ctx, turn)
	}
	return p.primary.Generate(ctx, turn)
}
