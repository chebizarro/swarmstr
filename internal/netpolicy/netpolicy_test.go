package netpolicy

import "testing"

func TestIsBlockedIP(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.1.1", "192.0.2.1", "fe80::1"} {
		if !IsBlockedIP(ip) {
			t.Fatalf("%s not blocked", ip)
		}
	}
	if IsBlockedIP("8.8.8.8") {
		t.Fatal("public IP blocked")
	}
}

func TestRedactSensitiveURL(t *testing.T) {
	got := RedactSensitiveURL("https://user:pass@example.com/path?token=abc&ok=1")
	if got != "https://example.com/path?ok=1&token=REDACTED" {
		t.Fatalf("redacted = %q", got)
	}
}

func TestAllowlistMatch(t *testing.T) {
	p, err := NormalizePolicy(Policy{AllowedDomains: []string{"API.Example.COM"}, AllowedCIDRs: []string{"8.8.8.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Allows("https://api.example.com/v1") || !p.Allows("8.8.8.8") {
		t.Fatal("expected allow")
	}
	if p.Allows("evil.example.com") || p.Allows("10.0.0.1") {
		t.Fatal("expected deny")
	}
}
