package manager

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"metiq/internal/secrets"
	"metiq/internal/store/state"
)

type runtimeHostConfig struct{ doc state.ConfigDoc }

func (c runtimeHostConfig) Get() state.ConfigDoc { return c.doc }

type runtimeCredentialStore struct {
	items map[string]secrets.MCPAuthCredential
}

func (s *runtimeCredentialStore) GetMCPCredential(key string) (secrets.MCPAuthCredential, bool) {
	credential, ok := s.items[key]
	return credential, ok
}
func (s *runtimeCredentialStore) PutMCPCredential(key string, credential secrets.MCPAuthCredential) error {
	if s.items == nil {
		s.items = map[string]secrets.MCPAuthCredential{}
	}
	s.items[key] = credential
	return nil
}
func (s *runtimeCredentialStore) DeleteMCPCredential(key string) (bool, error) {
	_, found := s.items[key]
	delete(s.items, key)
	return found, nil
}

func TestBuildHostWithRuntimeWiresConcreteSensitiveHosts(t *testing.T) {
	store := &runtimeCredentialStore{}
	evaluated := false
	requested := false
	host := BuildHostWithRuntime(runtimeHostConfig{}, nil, RuntimeServices{
		ExecApprovalEvaluate: func(_ context.Context, request map[string]any) (map[string]any, error) {
			evaluated = request["command"] == "ls"
			return map[string]any{"decision": "ask"}, nil
		},
		ExecApprovalRequest: func(_ context.Context, request map[string]any) (map[string]any, error) {
			requested = request["command"] == "ls"
			return map[string]any{"id": "approval-1"}, nil
		},
		ExecApprovalSnapshot: func() map[string]any { return map[string]any{} },
		ProviderCredentials:  store,
	})
	if host.Security == nil || host.ExecApproval == nil || host.Doctor == nil || host.ProviderAuth == nil {
		t.Fatalf("sensitive hosts not wired: %+v", host)
	}
	if _, err := host.ExecApproval.Evaluate(context.Background(), map[string]any{"command": "ls"}); err != nil || !evaluated {
		t.Fatalf("evaluate adapter err=%v evaluated=%v", err, evaluated)
	}
	if _, err := host.ExecApproval.Request(context.Background(), map[string]any{"command": "ls"}); err != nil || !requested {
		t.Fatalf("request adapter err=%v requested=%v", err, requested)
	}
}

func TestSecurityHostCheckPathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	result, err := (securityHostImpl{}).CheckPath(context.Background(), map[string]any{"root": root, "path": "escape/secret"})
	if err != nil {
		t.Fatal(err)
	}
	if allowed, _ := result["allowed"].(bool); allowed {
		t.Fatalf("symlink escape allowed: %+v", result)
	}
}

func TestProviderAuthHostScopesCredentialsAndDoesNotReturnSecrets(t *testing.T) {
	store := &runtimeCredentialStore{}
	base := &providerAuthHostImpl{store: store}
	hostA := *base
	hostA.pluginID = "plugin-a"
	result, err := hostA.Start(context.Background(), "example", map[string]any{"access_token": "secret-token", "scopes": []any{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	if result["access_token"] != nil || result["refresh_token"] != nil || result["client_secret"] != nil {
		t.Fatalf("provider auth leaked secret metadata: %+v", result)
	}
	if configured, _ := result["configured"].(bool); !configured {
		t.Fatalf("provider auth not configured: %+v", result)
	}
	if _, ok := store.items["plugin-provider-auth:plugin-a:example"]; !ok {
		t.Fatalf("credential not scoped to plugin: %+v", store.items)
	}
	hostB := hostA
	hostB.pluginID = "plugin-b"
	status, err := hostB.Status(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if configured, _ := status["configured"].(bool); configured {
		t.Fatalf("plugin B observed plugin A credential: %+v", status)
	}
}
