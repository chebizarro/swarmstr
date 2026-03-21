package toolbuiltin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/nostr/nip51"
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
				switch tc.name {
				case "nostr_list_put", "nostr_list_remove", "nostr_list_delete", "nostr_relay_list_set", "nostr_event_delete", "nostr_article_publish", "nostr_report":
					if body["ok"] != true {
						t.Fatalf("expected ok=true envelope for %s, got: %#v", tc.name, body)
					}
					if strings.TrimSpace(anyToString(body["event_id"])) == "" {
						t.Fatalf("expected event_id in %s result, got: %#v", tc.name, body)
					}
					if strings.TrimSpace(anyToString(body["tool"])) == "" {
						t.Fatalf("expected tool in %s result, got: %#v", tc.name, body)
					}
					if _, ok := body["kind"]; !ok {
						t.Fatalf("expected kind in %s result, got: %#v", tc.name, body)
					}
					if _, ok := body["targets"]; !ok {
						t.Fatalf("expected targets in %s result, got: %#v", tc.name, body)
					}
				}
			}
		})
	}
}

func TestRuntimeSmoke_NewNostrWriteHelpers_ErrorShape(t *testing.T) {
	signer := testSigner(t)
	tools := agent.NewToolRegistry()
	nostrOpts := NostrToolOpts{Keyer: signer}
	tools.RegisterWithDef("nostr_relay_list_set", NostrRelayListSetTool(nostrOpts), NostrRelayListSetDef)
	RegisterNIPTools(tools, nostrOpts)

	tests := []struct {
		name      string
		call      agent.ToolCall
		errPrefix string
	}{
		{
			name:      "nostr_event_delete missing ids",
			call:      agent.ToolCall{Name: "nostr_event_delete", Args: map[string]any{}},
			errPrefix: "nostr_event_delete_error:",
		},
		{
			name:      "nostr_report missing targets",
			call:      agent.ToolCall{Name: "nostr_report", Args: map[string]any{"report_type": "spam"}},
			errPrefix: "nostr_report_error:",
		},
		{
			name:      "nostr_article_publish missing fields",
			call:      agent.ToolCall{Name: "nostr_article_publish", Args: map[string]any{"title": "Only title"}},
			errPrefix: "nostr_article_publish_error:",
		},
		{
			name:      "nostr_relay_list_set no relays",
			call:      agent.ToolCall{Name: "nostr_relay_list_set", Args: map[string]any{}},
			errPrefix: "nostr_relay_list_set_error:",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := runToolCallThroughRuntime(t, tools, tc.call)
			tr := res.ToolTraces[0]
			if strings.TrimSpace(tr.Error) == "" {
				t.Fatalf("expected tool error, got none (trace=%+v)", tr)
			}
			if !strings.HasPrefix(strings.TrimSpace(tr.Error), tc.errPrefix) {
				t.Fatalf("expected machine-readable error prefix %q, got %q", tc.errPrefix, tr.Error)
			}
		})
	}
}

func anyToString(v any) string {
	s, _ := v.(string)
	return s
}
