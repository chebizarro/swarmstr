package toolbuiltin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"swarmstr/internal/agent"
	"swarmstr/internal/nostr/nip51"
)

func TestResolveSemanticListTarget_Defaults(t *testing.T) {
	kind, dtag, tag, err := resolveSemanticListTarget(map[string]any{"list_type": "follows"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kind != 3 || dtag != "" || tag != "p" {
		t.Fatalf("unexpected target kind=%d dtag=%q tag=%q", kind, dtag, tag)
	}
}

func TestResolveSemanticListTarget_AllowList(t *testing.T) {
	kind, dtag, tag, err := resolveSemanticListTarget(map[string]any{"list_type": "allow"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kind != 30000 || dtag != "allowlist" || tag != "p" {
		t.Fatalf("unexpected target kind=%d dtag=%q tag=%q", kind, dtag, tag)
	}
}

func TestResolveSemanticListTarget_KindOverride(t *testing.T) {
	kind, dtag, tag, err := resolveSemanticListTarget(map[string]any{"kind": float64(10002)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kind != 10002 || tag != "r" || dtag != "" {
		t.Fatalf("unexpected target kind=%d dtag=%q tag=%q", kind, dtag, tag)
	}
}

func decodeSemanticListErr(t *testing.T, err error) map[string]any {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	const prefix = "nostr_list_error:"
	if !strings.HasPrefix(msg, prefix) {
		t.Fatalf("missing prefix %q in error: %q", prefix, msg)
	}
	payload := strings.TrimPrefix(msg, prefix)
	var body map[string]any
	if jErr := json.Unmarshal([]byte(payload), &body); jErr != nil {
		t.Fatalf("invalid semantic list error JSON: %v (%q)", jErr, payload)
	}
	return body
}

func TestSemanticListErrors_AreMachineReadable(t *testing.T) {
	tools := agent.NewToolRegistry()
	RegisterNostrListSemanticTools(tools, NostrListToolOpts{Keyer: testSigner(t), Store: nip51.NewListStore()})

	cases := []agent.ToolCall{
		{Name: "nostr_list_put", Args: map[string]any{"list_type": "allow", "values": []any{"npub1x"}}},
		{Name: "nostr_list_get", Args: map[string]any{"list_type": "allow"}},
		{Name: "nostr_list_remove", Args: map[string]any{"list_type": "allow", "value": "npub1x"}},
		{Name: "nostr_list_delete", Args: map[string]any{"list_type": "allow"}},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			_, err := tools.Execute(context.Background(), tc)
			body := decodeSemanticListErr(t, err)
			if body["op"] == "" || body["code"] == "" || body["message"] == "" {
				t.Fatalf("expected op/code/message, got: %#v", body)
			}
		})
	}
}
