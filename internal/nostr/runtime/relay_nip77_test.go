package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/nostr/nip77"
)

func TestRelaySupportsNIP77WithClient(t *testing.T) {
	for _, tc := range []struct {
		name      string
		supported []int
		want      bool
	}{
		{"supported", []int{1, 11, 77}, true},
		{"unsupported", []int{1, 11, 42}, false},
		{"missing", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept") != "application/nostr+json" {
					t.Fatalf("unexpected Accept header %q", r.Header.Get("Accept"))
				}
				_ = json.NewEncoder(w).Encode(RelayInformation{SupportedNIPs: tc.supported})
			}))
			defer server.Close()
			got, err := RelaySupportsNIP77WithClient(context.Background(), server.Client(), server.URL, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("supported = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSyncRelayStateStopsBeforeNegotiationWhenUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(RelayInformation{SupportedNIPs: []int{1, 11}})
	}))
	defer server.Close()
	_, err := SyncRelayState(context.Background(), server.URL, nostr.Filter{Kinds: []nostr.Kind{1}}, nil, nil, nip77.SyncOptions{})
	if !errors.Is(err, ErrNIP77Unsupported) {
		t.Fatalf("error = %v, want ErrNIP77Unsupported", err)
	}
}

func TestRelaySupportsNIP77PropagatesDiscoveryErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusForbidden)
	}))
	defer server.Close()
	if _, err := RelaySupportsNIP77WithClient(context.Background(), server.Client(), server.URL, 0); err == nil {
		t.Fatal("expected discovery error")
	}
}
