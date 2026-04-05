package contextvm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
)

func TestListResourcesUsesResourcesListMethod(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	var gotMsg map[string]any
	var gotServerPubKey string
	var gotTimeout time.Duration
	var gotEncryption string
	sendContextVMRequestWithTimeout = func(_ context.Context, _ *nostr.Pool, _ nostr.Keyer, _ []string, serverPubKey string, msg map[string]any, timeout time.Duration, encryption string) (json.RawMessage, error) {
		gotMsg = msg
		gotServerPubKey = serverPubKey
		gotTimeout = timeout
		gotEncryption = encryption
		return json.RawMessage(`{"jsonrpc":"2.0","result":{"resources":[{"uri":"file:///tmp/test.txt","name":"test"}]}}`), nil
	}

	resources, err := ListResources(context.Background(), nil, nil, []string{"wss://relay.example"}, "peer-pubkey", "nip44")
	if err != nil {
		t.Fatalf("ListResources error: %v", err)
	}
	if gotServerPubKey != "peer-pubkey" {
		t.Fatalf("server pubkey = %q, want peer-pubkey", gotServerPubKey)
	}
	if gotTimeout != 30*time.Second {
		t.Fatalf("timeout = %v, want 30s", gotTimeout)
	}
	if gotEncryption != "nip44" {
		t.Fatalf("encryption = %q, want nip44", gotEncryption)
	}
	if gotMsg["method"] != "resources/list" {
		t.Fatalf("method = %#v, want resources/list", gotMsg["method"])
	}
	if len(resources) != 1 || resources[0]["uri"] != "file:///tmp/test.txt" {
		t.Fatalf("resources = %#v", resources)
	}
}

func TestGetPromptSendsArgumentsAndParsesResult(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	var gotParams map[string]any
	sendContextVMRequestWithTimeout = func(_ context.Context, _ *nostr.Pool, _ nostr.Keyer, _ []string, _ string, msg map[string]any, _ time.Duration, _ string) (json.RawMessage, error) {
		if msg["method"] != "prompts/get" {
			t.Fatalf("method = %#v, want prompts/get", msg["method"])
		}
		params, ok := msg["params"].(map[string]any)
		if !ok {
			t.Fatalf("params = %#v, want map[string]any", msg["params"])
		}
		gotParams = params
		return json.RawMessage(`{"jsonrpc":"2.0","result":{"messages":[{"role":"user","content":{"type":"text","text":"hello"}}]}}`), nil
	}

	result, err := GetPrompt(context.Background(), nil, nil, nil, "peer-pubkey", "review", map[string]any{"repo": "swarmstr"}, "auto")
	if err != nil {
		t.Fatalf("GetPrompt error: %v", err)
	}
	if gotParams["name"] != "review" {
		t.Fatalf("name = %#v, want review", gotParams["name"])
	}
	args, ok := gotParams["arguments"].(map[string]any)
	if !ok || args["repo"] != "swarmstr" {
		t.Fatalf("arguments = %#v", gotParams["arguments"])
	}
	messages, ok := result["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestListPromptsSurfacesServerError(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = func(_ context.Context, _ *nostr.Pool, _ nostr.Keyer, _ []string, _ string, _ map[string]any, _ time.Duration, _ string) (json.RawMessage, error) {
		return json.RawMessage(`{"jsonrpc":"2.0","error":{"message":"boom"}}`), nil
	}

	_, err := ListPrompts(context.Background(), nil, nil, nil, "peer-pubkey", "auto")
	if err == nil || !strings.Contains(err.Error(), "contextvm server error: boom") {
		t.Fatalf("err = %v, want server error", err)
	}
}
