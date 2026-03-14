package toolbuiltin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"swarmstr/internal/agent"
	"swarmstr/internal/nostr/nip51"
)

type scriptedProvider struct {
	calls []agent.ToolCall
}

func (p scriptedProvider) Generate(_ context.Context, _ agent.Turn) (agent.ProviderResult, error) {
	return agent.ProviderResult{ToolCalls: p.calls}, nil
}

func runToolCallThroughRuntime(t *testing.T, tools *agent.ToolRegistry, call agent.ToolCall) agent.TurnResult {
	t.Helper()
	rt, err := agent.NewProviderRuntime(scriptedProvider{calls: []agent.ToolCall{call}}, tools)
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	res, err := rt.ProcessTurn(context.Background(), agent.Turn{SessionID: "smoke-session", UserText: "run smoke"})
	if err != nil {
		t.Fatalf("process turn: %v", err)
	}
	if len(res.ToolTraces) != 1 {
		t.Fatalf("expected 1 tool trace, got %d", len(res.ToolTraces))
	}
	if res.ToolTraces[0].Call.Name != call.Name {
		t.Fatalf("trace name mismatch: got %s want %s", res.ToolTraces[0].Call.Name, call.Name)
	}
	return res
}

func TestRuntimeSmoke_NewNostrWriteHelpers(t *testing.T) {
	signer := testSigner(t)
	pk, err := signer.GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("get pubkey: %v", err)
	}

	tools := agent.NewToolRegistry()
	nostrOpts := NostrToolOpts{Keyer: signer}
	tools.RegisterWithDef("nostr_dm_decrypt", NostrDMDecryptTool(nostrOpts), NostrDMDecryptDef)
	tools.RegisterWithDef("nostr_relay_list_set", NostrRelayListSetTool(nostrOpts), NostrRelayListSetDef)
	RegisterNIPTools(tools, nostrOpts)

	listOpts := NostrListToolOpts{Keyer: signer, Store: nip51.NewListStore()}
	RegisterListTools(tools, listOpts)
	RegisterNostrListSemanticTools(tools, listOpts)

	tests := []struct {
		name string
		call agent.ToolCall
	}{
		{
			name: "nostr_dm_decrypt",
			call: agent.ToolCall{Name: "nostr_dm_decrypt", Args: map[string]any{"ciphertext": "abc", "sender_pubkey": pk.Hex(), "scheme": "nip04"}},
		},
		{
			name: "nostr_list_put",
			call: agent.ToolCall{Name: "nostr_list_put", Args: map[string]any{"list_type": "allow", "value": pk.Hex()}},
		},
		{
			name: "nostr_list_get",
			call: agent.ToolCall{Name: "nostr_list_get", Args: map[string]any{"list_type": "allow"}},
		},
		{
			name: "nostr_list_remove",
			call: agent.ToolCall{Name: "nostr_list_remove", Args: map[string]any{"list_type": "allow", "value": pk.Hex()}},
		},
		{
			name: "nostr_list_delete",
			call: agent.ToolCall{Name: "nostr_list_delete", Args: map[string]any{"list_type": "allow"}},
		},
		{
			name: "nostr_relay_list_set",
			call: agent.ToolCall{Name: "nostr_relay_list_set", Args: map[string]any{}},
		},
		{
			name: "nostr_event_delete",
			call: agent.ToolCall{Name: "nostr_event_delete", Args: map[string]any{"ids": []any{"deadbeef"}}},
		},
		{
			name: "nostr_article_publish",
			call: agent.ToolCall{Name: "nostr_article_publish", Args: map[string]any{"title": "Smoke", "content": "# Hello\n\nBody #nostr"}},
		},
		{
			name: "nostr_report",
			call: agent.ToolCall{Name: "nostr_report", Args: map[string]any{"report_type": "spam", "target_event_ids": []any{"deadbeef"}}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := runToolCallThroughRuntime(t, tools, tc.call)
			tr := res.ToolTraces[0]
			if strings.TrimSpace(tr.Result) == "" && strings.TrimSpace(tr.Error) == "" {
				t.Fatalf("tool returned neither result nor error: %+v", tr)
			}
			if strings.TrimSpace(tr.Result) != "" {
				var body map[string]any
				if err := json.Unmarshal([]byte(tr.Result), &body); err != nil {
					t.Fatalf("result is not valid JSON: %v (result=%q)", err, tr.Result)
				}
			}
		})
	}
}
