package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIRealtimeClientCreateSession(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer server-key" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("OpenAI-Safety-Identifier"); got != "hashed-user" {
			t.Errorf("safety identifier = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"client_secret":{"value":"ephemeral-secret"},"expires_at":1234}`))
	}))
	defer server.Close()

	client := &OpenAIRealtimeClient{HTTPClient: server.Client(), Endpoint: server.URL, OfferURL: "https://offer.example.test"}
	result, err := client.CreateSession(context.Background(), " server-key ", SessionConfig{
		Transport: TransportWebRTC, Model: "gpt-realtime-test", Voice: "marin", SafetyIdentifier: "hashed-user",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if result["clientSecret"] != "ephemeral-secret" || result["transport"] != TransportWebRTC {
		t.Fatalf("result = %#v", result)
	}
	if result["offerUrl"] != "https://offer.example.test" || result["offerResponseMaxBytes"] != OpenAIOfferResponseMaxBytes {
		t.Fatalf("offer metadata = %#v", result)
	}
	if result["expiresAt"] != int64(1234000) {
		t.Fatalf("expiresAt = %#v", result["expiresAt"])
	}
	session := gotBody["session"].(map[string]any)
	if session["type"] != "realtime" || session["model"] != "gpt-realtime-test" {
		t.Fatalf("session request = %#v", session)
	}
	audio := session["audio"].(map[string]any)
	output := audio["output"].(map[string]any)
	if output["voice"] != "marin" {
		t.Fatalf("voice = %#v", output["voice"])
	}
}

func TestOpenAIRealtimeClientDefaultsAndTopLevelValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"value":"ephemeral-secret"}`))
	}))
	defer server.Close()
	client := &OpenAIRealtimeClient{HTTPClient: server.Client(), Endpoint: server.URL}
	result, err := client.CreateSession(context.Background(), "key", SessionConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if result["model"] != DefaultOpenAIRealtimeModel || result["voice"] != DefaultOpenAIRealtimeVoice {
		t.Fatalf("defaults = %#v", result)
	}
}

func TestOpenAIRealtimeClientRejectsUnsupportedTransportAndSanitizesErrors(t *testing.T) {
	client := &OpenAIRealtimeClient{}
	if _, err := client.CreateSession(context.Background(), "key", SessionConfig{Transport: TransportProviderWebSocket}); err == nil {
		t.Fatal("expected unsupported transport error")
	}

	secret := "must-not-leak-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, `{"error":{"message":"bad token %s"}}`, secret)
	}))
	defer server.Close()
	client = &OpenAIRealtimeClient{HTTPClient: server.Client(), Endpoint: server.URL}
	_, err := client.CreateSession(context.Background(), "key", SessionConfig{Transport: TransportWebRTC})
	if err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked provider body: %v", err)
	}
}

func TestOpenAIRealtimeClientBoundsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"value":"` + strings.Repeat("x", openAISecretResponseMaxBytes) + `"}`))
	}))
	defer server.Close()
	client := &OpenAIRealtimeClient{HTTPClient: server.Client(), Endpoint: server.URL}
	_, err := client.CreateSession(context.Background(), "key", SessionConfig{Transport: TransportWebRTC})
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("error = %v", err)
	}
}

func TestSupportsTransport(t *testing.T) {
	provider := fakeTransportProvider{transports: []string{TransportWebRTC}}
	if supported, advertised := SupportsTransport(provider, TransportWebRTC); !supported || !advertised {
		t.Fatalf("supported=%v advertised=%v", supported, advertised)
	}
	if supported, advertised := SupportsTransport(provider, TransportProviderWebSocket); supported || !advertised {
		t.Fatalf("supported=%v advertised=%v", supported, advertised)
	}
	if _, advertised := SupportsTransport(struct{}{}, TransportWebRTC); advertised {
		t.Fatal("plain provider should not advertise transports")
	}
}

type fakeTransportProvider struct{ transports []string }

func (f fakeTransportProvider) BrowserTransports() []string { return f.transports }
