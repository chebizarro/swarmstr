package channels

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"metiq/internal/plugins/sdk"
	"metiq/internal/secrets"
	"metiq/internal/store/state"
)

type protectedTestBackend struct {
	mu    sync.Mutex
	items map[string]string
}

func (b *protectedTestBackend) Name() string          { return "protected-test" }
func (b *protectedTestBackend) ProtectedAtRest() bool { return true }
func (b *protectedTestBackend) Get(key string) (string, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	value, ok := b.items[key]
	return value, ok, nil
}
func (b *protectedTestBackend) Set(key, value string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.items[key] = value
	return nil
}
func (b *protectedTestBackend) Delete(key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.items, key)
	return nil
}

type credentialTestPlugin struct {
	connects int
	cfg      map[string]any
	writer   sdk.ChannelCredentialWriter
}

func (p *credentialTestPlugin) ID() string                   { return "credential-test" }
func (p *credentialTestPlugin) Type() string                 { return "Credential Test" }
func (p *credentialTestPlugin) ConfigSchema() map[string]any { return nil }
func (p *credentialTestPlugin) Connect(ctx context.Context, id string, cfg map[string]any, _ func(sdk.InboundChannelMessage)) (sdk.ChannelHandle, error) {
	p.connects++
	p.cfg = cfg
	p.writer = sdk.ChannelCredentialWriterFrom(ctx)
	return &lifecycleTestHandle{id: id}, nil
}

func newProtectedTestStore(t *testing.T) *secrets.Store {
	t.Helper()
	store := secrets.NewStore([]string{t.TempDir() + "/.env"})
	store.SetMCPAuthPath(t.TempDir() + "/mcp-auth.json")
	store.SetBackend(&protectedTestBackend{items: map[string]string{}})
	return store
}

func TestAccountRuntimeResolvesStoredSecretRefAndPersistsRotation(t *testing.T) {
	store := newProtectedTestStore(t)
	if _, err := store.SetStoredSecret("CRED_TEST_REFRESH", "rt-1", secrets.StoredSecretKindSecret, []string{"oauth.example"}, "operator"); err != nil {
		t.Fatalf("seed stored secret: %v", err)
	}
	ref := secrets.StoredSecretRef("CRED_TEST_REFRESH")

	plugin := &credentialTestPlugin{}
	RegisterChannelPlugin(plugin)
	ConfigureChannelAccounts(state.NostrChannelsConfig{
		"acct": {Kind: plugin.ID(), Enabled: true, Config: map[string]any{
			"refresh_token": map[string]any{"source": string(ref.Source), "provider": ref.Provider, "id": ref.ID},
			"app_id":        "literal-app",
		}},
	})
	t.Cleanup(func() { ConfigureChannelAccounts(nil) })

	runtime := NewAccountRuntime(AccountRuntimeOptions{Secrets: store})
	if _, err := runtime.Start(context.Background(), plugin.ID(), "acct"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if got := plugin.cfg["refresh_token"]; got != "rt-1" {
		t.Fatalf("connect refresh_token = %#v, want resolved stored value", got)
	}
	if got := plugin.cfg["app_id"]; got != "literal-app" {
		t.Fatalf("connect app_id = %#v", got)
	}
	account, err := ResolveConfiguredChannelAccount(plugin.ID(), "acct")
	if err != nil {
		t.Fatal(err)
	}
	if _, isRef := account.Config["refresh_token"].(map[string]any); !isRef {
		t.Fatalf("account registry must keep the ref, not the resolved value: %#v", account.Config["refresh_token"])
	}

	if plugin.writer == nil {
		t.Fatal("connect ctx carries no credential writer")
	}
	if err := plugin.writer.PersistCredential(context.Background(), "refresh_token", "rt-2"); err != nil {
		t.Fatalf("persist rotated credential: %v", err)
	}
	if got, err := secrets.NewLifecycle(store).ResolveRef(context.Background(), ref); err != nil || got != "rt-2" {
		t.Fatalf("stored value after rotation = %q, %v", got, err)
	}
	records, err := store.ListStoredSecrets()
	if err != nil || len(records) != 1 {
		t.Fatalf("records = %#v, %v", records, err)
	}
	if rec := records[0]; rec.Kind != secrets.StoredSecretKindSecret || len(rec.AllowedHosts) != 1 || rec.UpdatedBy != "channel:credential-test/acct" {
		t.Fatalf("rotation must preserve kind/allowed hosts and attribute the channel: %#v", rec.StoredSecretMetadata)
	}
	if err := plugin.writer.PersistCredential(context.Background(), "app_id", "x"); !errors.Is(err, sdk.ErrCredentialNotWritable) {
		t.Fatalf("literal field write-back err = %v, want ErrCredentialNotWritable", err)
	}

	// A restart reconnects with the rotated value.
	runtime.CloseAll()
	if _, err := runtime.Start(context.Background(), plugin.ID(), "acct"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if got := plugin.cfg["refresh_token"]; got != "rt-2" {
		t.Fatalf("reconnect refresh_token = %#v, want rotated value", got)
	}
	runtime.CloseAll()
}

func TestAccountRuntimeFailsClosedOnUnresolvableSecretRef(t *testing.T) {
	plugin := &credentialTestPlugin{}
	RegisterChannelPlugin(&credentialTestPluginAlias{plugin})
	ConfigureChannelAccounts(state.NostrChannelsConfig{
		"acct": {Kind: "credential-test-missing", Enabled: true, Config: map[string]any{
			"refresh_token": map[string]any{"source": "store", "provider": "gateway-store", "id": "MISSING_SECRET"},
		}},
	})
	t.Cleanup(func() { ConfigureChannelAccounts(nil) })

	runtime := NewAccountRuntime(AccountRuntimeOptions{Secrets: newProtectedTestStore(t)})
	snapshot, err := runtime.Start(context.Background(), "credential-test-missing", "acct")
	if err == nil || snapshot.State != AccountFailed {
		t.Fatalf("start = %#v, %v; want failure", snapshot, err)
	}
	if plugin.connects != 0 {
		t.Fatalf("plugin connected %d times with an unresolved secret ref", plugin.connects)
	}
}

type credentialTestPluginAlias struct{ *credentialTestPlugin }

func (credentialTestPluginAlias) ID() string { return "credential-test-missing" }

func TestAccountScopedGatewayMethodResolvesSecretRefFromContextStore(t *testing.T) {
	store := newProtectedTestStore(t)
	if _, err := store.SetStoredSecret("ACTION_BOT_TOKEN", "xoxb-resolved", secrets.StoredSecretKindSecret, nil, "operator"); err != nil {
		t.Fatalf("seed stored secret: %v", err)
	}
	ref := secrets.StoredSecretRef("ACTION_BOT_TOKEN")
	refValue := map[string]any{"source": string(ref.Source), "provider": ref.Provider, "id": ref.ID}
	ConfigureChannelAccounts(state.NostrChannelsConfig{
		"work": {Kind: "slack", Config: map[string]any{"bot_token": refValue}},
	})
	t.Cleanup(func() { ConfigureChannelAccounts(nil) })

	var received map[string]any
	methods := AccountScopedGatewayMethods("slack", []sdk.GatewayMethod{{
		Method: "slack.test",
		Handle: func(_ context.Context, params map[string]any) (map[string]any, error) {
			received = params
			return map[string]any{"ok": true}, nil
		},
	}})
	// A caller-supplied ref is passed through verbatim, never dereferenced.
	ctx := WithAccountSecrets(context.Background(), store)
	if _, err := methods[0].Handle(ctx, map[string]any{"text": "hi", "note": refValue}); err != nil {
		t.Fatalf("wrapped handle: %v", err)
	}
	if received["bot_token"] != "xoxb-resolved" {
		t.Fatalf("handler got bot_token %#v, want resolved secret", received["bot_token"])
	}
	if _, isRef := received["note"].(map[string]any); !isRef {
		t.Fatalf("caller-supplied ref was dereferenced: %#v", received["note"])
	}
	account, err := ResolveConfiguredChannelAccount("slack", "work")
	if err != nil {
		t.Fatalf("resolve account: %v", err)
	}
	if _, isRef := account.Config["bot_token"].(map[string]any); !isRef {
		t.Fatalf("account registry kept resolved value instead of ref: %#v", account.Config["bot_token"])
	}
}

func TestAccountScopedGatewayMethodFailsClosedOnUnresolvableSecretRef(t *testing.T) {
	ConfigureChannelAccounts(state.NostrChannelsConfig{
		"work": {Kind: "slack", Config: map[string]any{
			"bot_token": map[string]any{"source": "store", "provider": "gateway-store", "id": "MISSING_SECRET"},
		}},
	})
	t.Cleanup(func() { ConfigureChannelAccounts(nil) })

	called := false
	methods := AccountScopedGatewayMethods("slack", []sdk.GatewayMethod{{
		Method: "slack.test",
		Handle: func(context.Context, map[string]any) (map[string]any, error) {
			called = true
			return nil, nil
		},
	}})
	for name, ctx := range map[string]context.Context{
		"missing entry": WithAccountSecrets(context.Background(), newProtectedTestStore(t)),
		"no store":      context.Background(),
	} {
		_, err := methods[0].Handle(ctx, map[string]any{"text": "hi"})
		if err == nil || !strings.Contains(err.Error(), `config field "bot_token"`) {
			t.Fatalf("%s: expected fail-closed secret ref error, got %v", name, err)
		}
	}
	if called {
		t.Fatal("handler ran with an unresolvable secret ref")
	}
}
