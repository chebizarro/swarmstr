package toolgrpc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/config"

	"google.golang.org/grpc/metadata"
)

func TestConnectionManagerMetadataSourceHexRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin.macaroon")
	if err := os.WriteFile(path, []byte{0x00, 0xab, 0xff}, 0o600); err != nil {
		t.Fatalf("write macaroon: %v", err)
	}
	manager, err := NewConnectionManagerWithOptions(
		[]config.GRPCEndpointConfig{{ID: "lnd:primary", Target: "127.0.0.1:1"}},
		WithMetadataSources(map[string]map[string]CredentialSource{
			"lnd:primary": {"macaroon": {Ref: "file:" + path, Encoding: "hex"}},
		}),
	)
	if err != nil {
		t.Fatalf("NewConnectionManagerWithOptions: %v", err)
	}

	assertMacaroon := func(want string) {
		t.Helper()
		ctx, cancel, err := manager.CallContext(context.Background(), "lnd:primary", CallOptions{})
		if err != nil {
			t.Fatalf("CallContext: %v", err)
		}
		defer cancel()
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			t.Fatal("missing outgoing metadata")
		}
		if got := md.Get("macaroon"); len(got) != 1 || got[0] != want {
			t.Fatalf("macaroon metadata = %v, want %q", got, want)
		}
	}

	assertMacaroon("00abff")
	if err := os.WriteFile(path, []byte{0x10, 0x20}, 0o600); err != nil {
		t.Fatalf("rotate macaroon: %v", err)
	}
	assertMacaroon("1020")
}

func TestConnectionManagerCredentialSourceFailuresAreBoundedAndSanitized(t *testing.T) {
	oversized := filepath.Join(t.TempDir(), "oversized.macaroon")
	if err := os.WriteFile(oversized, make([]byte, MaxCredentialSourceBytes+1), 0o600); err != nil {
		t.Fatalf("write oversized credential: %v", err)
	}
	nonASCII := filepath.Join(t.TempDir(), "binary.macaroon")
	if err := os.WriteFile(nonASCII, []byte{0x00, 0xff}, 0o600); err != nil {
		t.Fatalf("write binary credential: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "super-secret-do-not-leak.macaroon")

	tests := []struct {
		name   string
		source CredentialSource
		leak   string
	}{
		{name: "relative file", source: CredentialSource{Ref: "file:relative.macaroon", Encoding: "hex"}, leak: "relative.macaroon"},
		{name: "missing file", source: CredentialSource{Ref: "file:" + missing, Encoding: "hex"}, leak: missing},
		{name: "oversized file", source: CredentialSource{Ref: "file:" + oversized, Encoding: "hex"}, leak: oversized},
		{name: "non ascii text", source: CredentialSource{Ref: "file:" + nonASCII, Encoding: "text"}, leak: nonASCII},
		{name: "unknown encoding", source: CredentialSource{Ref: "file:" + nonASCII, Encoding: "base64"}, leak: nonASCII},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, err := NewConnectionManagerWithOptions(
				[]config.GRPCEndpointConfig{{ID: "secured", Target: "127.0.0.1:1"}},
				WithMetadataSources(map[string]map[string]CredentialSource{
					"secured": {"macaroon": test.source},
				}),
			)
			if err != nil {
				t.Fatalf("NewConnectionManagerWithOptions: %v", err)
			}
			_, cancel, err := manager.CallContext(context.Background(), "secured", CallOptions{})
			if cancel != nil {
				cancel()
			}
			if err == nil {
				t.Fatal("CallContext accepted invalid credential source")
			}
			if test.leak != "" && strings.Contains(err.Error(), test.leak) {
				t.Fatalf("credential error leaked source path: %v", err)
			}
		})
	}
}

func TestConnectionManagerUsesInjectedResolverOnEveryCall(t *testing.T) {
	value := "first"
	resolver := ValueResolverFunc(func(context.Context, CredentialSource) ([]byte, error) {
		return []byte(value), nil
	})
	manager, err := NewConnectionManagerWithOptions(
		[]config.GRPCEndpointConfig{{ID: "tapd:assets", Target: "127.0.0.1:1"}},
		WithValueResolver(resolver),
		WithMetadataSources(map[string]map[string]CredentialSource{
			"tapd:assets": {"macaroon": {Ref: "secret:TAPD_MACAROON", Encoding: "text"}},
		}),
	)
	if err != nil {
		t.Fatalf("NewConnectionManagerWithOptions: %v", err)
	}
	read := func() string {
		ctx, cancel, err := manager.CallContext(context.Background(), "tapd:assets", CallOptions{})
		if err != nil {
			t.Fatalf("CallContext: %v", err)
		}
		defer cancel()
		md, _ := metadata.FromOutgoingContext(ctx)
		return md.Get("macaroon")[0]
	}
	if got := read(); got != "first" {
		t.Fatalf("first metadata = %q", got)
	}
	value = "second"
	if got := read(); got != "second" {
		t.Fatalf("rotated metadata = %q", got)
	}
}
