package main

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	mcppkg "metiq/internal/mcp"
)

const memorySessionBootstrapPromptName = "memory_session_bootstrap"

type memoryBootstrapPromptClient interface {
	ListServerStates() []mcppkg.ServerStateSnapshot
	ListPrompts(context.Context, string) (*sdkmcp.ListPromptsResult, error)
	GetPrompt(context.Context, string, string, map[string]string) (*sdkmcp.GetPromptResult, error)
}

type memorySessionBootstrapper struct {
	client func() memoryBootstrapPromptClient
	once   sync.Map // sessionID -> bootstrap fragment string
}

func (b *memorySessionBootstrapper) prepend(ctx context.Context, sessionID, agentID, systemPrompt string) string {
	if b == nil {
		return systemPrompt
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "default"
	}
	if cached, ok := b.once.Load(sessionID); ok {
		return joinPromptSections(cached.(string), systemPrompt)
	}
	fragment := b.fetch(ctx, agentID)
	b.once.Store(sessionID, fragment)
	return joinPromptSections(fragment, systemPrompt)
}

func (b *memorySessionBootstrapper) fetch(ctx context.Context, agentID string) string {
	if b == nil || b.client == nil {
		return ""
	}
	client := b.client()
	if client == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	serverName := findMemoryBootstrapPromptServer(ctx, client)
	if serverName == "" {
		return ""
	}
	result, err := client.GetPrompt(ctx, serverName, memorySessionBootstrapPromptName, map[string]string{
		"agent-identity-scope": strings.TrimSpace(agentID),
	})
	if err != nil {
		log.Printf("memory bootstrap: prompts/get %s/%s skipped: %v", serverName, memorySessionBootstrapPromptName, err)
		return ""
	}
	return extractPromptText(result)
}

func findMemoryBootstrapPromptServer(ctx context.Context, client memoryBootstrapPromptClient) string {
	for _, state := range client.ListServerStates() {
		if state.State != mcppkg.ConnectionStateConnected || !state.Capabilities.Prompts {
			continue
		}
		result, err := client.ListPrompts(ctx, state.Name)
		if err != nil {
			continue
		}
		for _, prompt := range result.Prompts {
			if prompt != nil && strings.TrimSpace(prompt.Name) == memorySessionBootstrapPromptName {
				return state.Name
			}
		}
	}
	return ""
}

func extractPromptText(result *sdkmcp.GetPromptResult) string {
	if result == nil {
		return ""
	}
	parts := make([]string, 0, len(result.Messages))
	for _, msg := range result.Messages {
		if msg == nil {
			continue
		}
		switch content := msg.Content.(type) {
		case *sdkmcp.TextContent:
			if text := strings.TrimSpace(content.Text); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

var memoryBootstrapper *memorySessionBootstrapper
