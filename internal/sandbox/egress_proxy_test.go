package sandbox

import (
	"context"
	"encoding/base64"
	"net/netip"
	"strings"
	"testing"

	"metiq/internal/netpolicy"
)

type staticEgressResolver map[string][]netip.Addr

func (r staticEgressResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return append([]netip.Addr{}, r[host]...), nil
}

func newTestEgressProxy(t *testing.T, domains, cidrs []string, answers staticEgressResolver) *sandboxEgressProxy {
	t.Helper()
	policy, err := netpolicy.NormalizePolicy(netpolicy.Policy{AllowedDomains: domains, AllowedCIDRs: cidrs})
	if err != nil {
		t.Fatal(err)
	}
	return &sandboxEgressProxy{policy: policy, resolver: answers, token: "secret"}
}

func TestSandboxEgressProxyAuthorizesAndPinsAllowedDomain(t *testing.T) {
	proxy := newTestEgressProxy(t, []string{"api.example.com"}, nil, staticEgressResolver{
		"api.example.com": {netip.MustParseAddr("93.184.216.34")},
		"other.example":   {netip.MustParseAddr("93.184.216.35")},
	})
	got, err := proxy.authorizeTarget(context.Background(), "api.example.com:443")
	if err != nil || got != "93.184.216.34:443" {
		t.Fatalf("allowed target = %q err=%v", got, err)
	}
	if _, err := proxy.authorizeTarget(context.Background(), "other.example:443"); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("expected non-allowlisted domain denial, got %v", err)
	}
}

func TestSandboxEgressProxyRejectsPrivateDNSRebinding(t *testing.T) {
	proxy := newTestEgressProxy(t, []string{"api.example.com"}, nil, staticEgressResolver{
		"api.example.com": {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.8")},
	})
	if _, err := proxy.authorizeTarget(context.Background(), "api.example.com:443"); err == nil || !strings.Contains(err.Error(), "blocked address") {
		t.Fatalf("expected private answer denial, got %v", err)
	}
}

func TestSandboxEgressProxyAllowsPublicCIDRAndRejectsPrivateLiteral(t *testing.T) {
	proxy := newTestEgressProxy(t, nil, []string{"93.184.216.0/24", "10.0.0.0/8"}, staticEgressResolver{})
	if got, err := proxy.authorizeTarget(context.Background(), "93.184.216.99:80"); err != nil || got != "93.184.216.99:80" {
		t.Fatalf("allowed CIDR target = %q err=%v", got, err)
	}
	if _, err := proxy.authorizeTarget(context.Background(), "10.0.0.8:80"); err == nil || !strings.Contains(err.Error(), "blocked address") {
		t.Fatalf("private CIDR must remain blocked, got %v", err)
	}
}

func TestSandboxEgressProxyRequiresPerRunCredential(t *testing.T) {
	proxy := &sandboxEgressProxy{token: "secret"}
	valid := "Basic " + base64.StdEncoding.EncodeToString([]byte("metiq:secret"))
	if !proxy.authenticated(valid) {
		t.Fatal("valid proxy credential rejected")
	}
	for _, header := range []string{"", "Bearer secret", "Basic " + base64.StdEncoding.EncodeToString([]byte("metiq:wrong"))} {
		if proxy.authenticated(header) {
			t.Fatalf("invalid proxy credential accepted: %q", header)
		}
	}
}
