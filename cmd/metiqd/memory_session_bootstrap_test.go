package main

import (
	"context"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	mcppkg "metiq/internal/mcp"
)

type fakeMemoryBootstrapClient struct {
	states   []mcppkg.ServerStateSnapshot
	fragment string
	args     map[string]string
	gets     int
}

func (f *fakeMemoryBootstrapClient) ListServerStates() []mcppkg.ServerStateSnapshot { return f.states }
func (f *fakeMemoryBootstrapClient) ListPrompts(context.Context, string) (*sdkmcp.ListPromptsResult, error) {
	return &sdkmcp.ListPromptsResult{Prompts: []*sdkmcp.Prompt{{Name: memorySessionBootstrapPromptName}}}, nil
}
func (f *fakeMemoryBootstrapClient) GetPrompt(_ context.Context, _ string, _ string, args map[string]string) (*sdkmcp.GetPromptResult, error) {
	f.gets++
	f.args = args
	return &sdkmcp.GetPromptResult{Messages: []*sdkmcp.PromptMessage{{
		Role:    sdkmcp.Role("system"),
		Content: &sdkmcp.TextContent{Text: f.fragment},
	}}}, nil
}

func TestMemorySessionBootstrapPrependsPromptFragment(t *testing.T) {
	client := &fakeMemoryBootstrapClient{
		states: []mcppkg.ServerStateSnapshot{{
			Name:         "agent-memory",
			State:        mcppkg.ConnectionStateConnected,
			Capabilities: mcppkg.CapabilitySnapshot{Prompts: true},
		}},
		fragment: "## Memory Bootstrap\nSearch before acting.",
	}
	bootstrapper := &memorySessionBootstrapper{client: func() memoryBootstrapPromptClient { return client }}

	got := bootstrapper.prepend(context.Background(), "sess-1", "builder", "base system")
	want := "## Memory Bootstrap\nSearch before acting.\n\nbase system"
	if got != want {
		t.Fatalf("prompt = %q, want %q", got, want)
	}
	if client.args["agent-identity-scope"] != "builder" {
		t.Fatalf("agent identity scope argument = %#v", client.args)
	}

	got = bootstrapper.prepend(context.Background(), "sess-1", "builder", "next system")
	if got != "## Memory Bootstrap\nSearch before acting.\n\nnext system" {
		t.Fatalf("cached prompt = %q", got)
	}
	if client.gets != 1 {
		t.Fatalf("GetPrompt calls = %d, want 1", client.gets)
	}
}

func TestMemorySessionBootstrapSkipsWhenUnconfigured(t *testing.T) {
	client := &fakeMemoryBootstrapClient{}
	bootstrapper := &memorySessionBootstrapper{client: func() memoryBootstrapPromptClient { return client }}

	got := bootstrapper.prepend(context.Background(), "sess-unconfigured", "builder", "base system")
	if got != "base system" {
		t.Fatalf("prompt = %q, want base system", got)
	}
	if client.gets != 0 {
		t.Fatalf("GetPrompt calls = %d, want 0", client.gets)
	}
}
