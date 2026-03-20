package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	copilot "github.com/github/copilot-sdk/go"
)

// CopilotCLIChatProvider implements ChatProvider using the GitHub Copilot CLI SDK.
// It connects to a local Copilot CLI server (spawned or pre-existing) via JSON-RPC
// and uses Session.SendAndWait for single-call interactions.
//
// This is distinct from the existing "github-copilot" model which uses the
// OpenAI-compatible API at api.githubcopilot.com. Use "copilot-cli" model
// to activate this provider.
type CopilotCLIChatProvider struct {
	// Model is the Copilot model name (e.g. "gpt-4.1", "claude-sonnet-4").
	Model string
	// CLIURL is the URL of an external Copilot CLI server.
	// If empty, the SDK spawns its own embedded CLI process.
	CLIURL string

	mu      sync.Mutex
	client  *copilot.Client
	session *copilot.Session
}

// ensureSession lazily initialises the Copilot client and session.
func (p *CopilotCLIChatProvider) ensureSession(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session != nil {
		return nil
	}

	opts := &copilot.ClientOptions{}
	if p.CLIURL != "" {
		opts.CLIUrl = p.CLIURL
	}
	client := copilot.NewClient(opts)
	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("copilot-cli: start failed: %w", err)
	}

	model := p.Model
	if model == "" {
		model = "gpt-4.1"
	}

	session, err := client.CreateSession(ctx, &copilot.SessionConfig{
		Model: model,
		Hooks: &copilot.SessionHooks{},
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
	})
	if err != nil {
		client.Stop()
		return fmt.Errorf("copilot-cli: create session failed: %w", err)
	}

	p.client = client
	p.session = session
	return nil
}

// Chat implements ChatProvider.
func (p *CopilotCLIChatProvider) Chat(ctx context.Context, messages []LLMMessage, tools []ToolDefinition, opts ChatOptions) (*LLMResponse, error) {
	if err := p.ensureSession(ctx); err != nil {
		return nil, err
	}

	// Build a prompt from the message history.
	// The Copilot CLI SDK takes a single prompt string; we serialise
	// the conversation as JSON so the model receives full context.
	type promptMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	out := make([]promptMsg, 0, len(messages))
	for _, m := range messages {
		out = append(out, promptMsg{Role: m.Role, Content: m.Content})
	}
	prompt, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("copilot-cli: marshal messages: %w", err)
	}

	p.mu.Lock()
	session := p.session
	p.mu.Unlock()

	if session == nil {
		return nil, fmt.Errorf("copilot-cli: session closed")
	}

	resp, err := session.SendAndWait(ctx, copilot.MessageOptions{
		Prompt: string(prompt),
	})
	if err != nil {
		return nil, fmt.Errorf("copilot-cli: send failed: %w", err)
	}

	if resp == nil || resp.Data.Content == nil {
		return nil, fmt.Errorf("copilot-cli: empty response")
	}

	return &LLMResponse{
		Content: *resp.Data.Content,
	}, nil
}

// CopilotCLIProvider implements Provider using the Copilot CLI SDK.
// It delegates to CopilotCLIChatProvider via generateWithAgenticLoop.
type CopilotCLIProvider struct {
	ChatProvider *CopilotCLIChatProvider
}

// Generate implements Provider.
func (p *CopilotCLIProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	return generateWithAgenticLoop(ctx, p.ChatProvider, turn, "", "copilot-cli")
}

// Close shuts down the Copilot client and session.
func (p *CopilotCLIChatProvider) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session != nil {
		_ = p.session.Disconnect()
		p.session = nil
	}
	if p.client != nil {
		p.client.Stop()
		p.client = nil
	}
}
