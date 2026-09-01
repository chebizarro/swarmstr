package realtimevoice

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	browserpkg "metiq/internal/browser"
)

func TestOpenAIRealtimeProviderCreatesBrowserSession(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "server-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer server-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"client_secret":{"value":"ephemeral"},"expires_at":123}`))
	}))
	defer server.Close()

	provider := NewOpenAIRealtimeProvider()
	provider.browserClient = &browserpkg.OpenAIRealtimeClient{HTTPClient: server.Client(), Endpoint: server.URL}
	session, err := provider.CreateBrowserSession(context.Background(), browserpkg.SessionConfig{
		Transport: browserpkg.TransportWebRTC,
		Model:     "gpt-test",
		Voice:     "marin",
	})
	if err != nil {
		t.Fatalf("create browser session: %v", err)
	}
	if session["provider"] != provider.ID() || session["transport"] != browserpkg.TransportWebRTC || session["clientSecret"] != "ephemeral" {
		t.Fatalf("session = %#v", session)
	}
	if transports := provider.BrowserTransports(); len(transports) != 1 || transports[0] != browserpkg.TransportWebRTC {
		t.Fatalf("transports = %#v", transports)
	}
}

func TestRealtimeWebSocketProviderRejectsUnsupportedBrowserTransport(t *testing.T) {
	provider := NewElevenLabsRealtimeProvider()
	if _, err := provider.CreateBrowserSession(context.Background(), browserpkg.SessionConfig{Transport: browserpkg.TransportProviderWebSocket}); err == nil {
		t.Fatal("expected unsupported browser session")
	}
	if supported, advertised := browserpkg.SupportsTransport(provider, browserpkg.TransportProviderWebSocket); supported || !advertised {
		t.Fatalf("supported=%v advertised=%v", supported, advertised)
	}
}

type browserPluginInvoker struct {
	calls []string
}

func (i *browserPluginInvoker) InvokeProvider(_ context.Context, _ string, method string, params any) (any, error) {
	i.calls = append(i.calls, method)
	if method == "createBrowserSession" {
		return nil, fmt.Errorf("unknown provider method")
	}
	m := params.(map[string]any)
	return map[string]any{"transport": m["transport"], "clientSecret": "plugin-secret"}, nil
}

func TestPluginProviderCreatesBrowserSessionWithMethodFallback(t *testing.T) {
	host := &browserPluginInvoker{}
	provider := NewPluginProvider("plugin", map[string]any{
		"capabilities": map[string]any{"transports": []any{browserpkg.TransportProviderWebSocket}},
	}, host)
	session, err := provider.CreateBrowserSession(context.Background(), browserpkg.SessionConfig{
		Transport: browserpkg.TransportProviderWebSocket,
		Model:     "model",
	})
	if err != nil {
		t.Fatalf("create plugin browser session: %v", err)
	}
	if session["clientSecret"] != "plugin-secret" || len(host.calls) != 2 || host.calls[1] != "create_browser_session" {
		t.Fatalf("session=%#v calls=%#v", session, host.calls)
	}
	if supported, advertised := browserpkg.SupportsTransport(provider, browserpkg.TransportProviderWebSocket); !supported || !advertised {
		t.Fatalf("supported=%v advertised=%v", supported, advertised)
	}
}
