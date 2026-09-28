package channels

import (
	"context"
	"errors"
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
