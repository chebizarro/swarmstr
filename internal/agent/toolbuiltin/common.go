// Package toolbuiltin provides built-in agent tool implementations that are
// registered at daemon startup.  Each exported function returns an
// agent.ToolFunc that can be passed directly to agent.ToolRegistry.Register.
package toolbuiltin

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"metiq/internal/browser"
)

// IsPrivateHost reports whether host (with optional :port) resolves to a
// loopback or RFC1918/ULA private address.  Hostname strings that cannot be
// parsed as IP addresses are treated as public (no DNS lookup is performed).
func IsPrivateHost(host string) bool {
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "localhost" || hostname == "::1" {
		return true
	}
	ip := net.ParseIP(hostname)
	if ip == nil {
		return false
	}
	return isPrivateIP(ip)
}

func isPrivateIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	private := []string{
		"0.0.0.0/8",
		"100.64.0.0/10", // carrier-grade NAT
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16", // link-local
		"::1/128",
		"fc00::/7",                                          // IPv6 ULA
		"fe80::/10",                                         // IPv6 link-local
		"198.18.0.0/15",                                     // benchmark networks
		"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", // documentation
		"2001:db8::/32", // IPv6 documentation
	}
	for _, cidr := range private {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

// HasPrivateResolvedIP reports whether host resolves to a private or loopback
// address. Literal IP hosts are checked directly. Hostnames are DNS-resolved
// and rejected if any result lands in a private range.
func HasPrivateResolvedIP(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return isPrivateIP(ip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		// Fail closed on DNS errors to avoid bypassing SSRF checks via
		// unresolvable/intermittent names.
		return true
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return true
		}
	}
	return false
}

// NewFetchBrowserClient creates a redirect-safe browser client whose DNS
// result is validated again and pinned at dial time. allowLocal intentionally
// disables both URL-time and dial-time address restrictions.
func NewFetchBrowserClient(base *http.Client, allowLocal bool) *browser.Client {
	client := &browser.Client{
		HTTPClient: base,
		ValidateURL: func(rawURL string) error {
			return ValidateFetchURL(rawURL, allowLocal)
		},
	}
	if !allowLocal {
		client.ValidateIP = func(ip net.IP) error {
			if isPrivateIP(ip) {
				return fmt.Errorf("access to non-public network addresses is disabled")
			}
			return nil
		}
	}
	return client
}

// ValidateFetchURL checks that rawURL is acceptable as a web-fetch target.
// It rejects empty URLs, non-http/https schemes, and (unless allowLocal is
// true) non-public hosts.
func ValidateFetchURL(rawURL string, allowLocal bool) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return fmt.Errorf("url is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("only http/https URLs are supported (got %q)", parsed.Scheme)
	}
	hostname := strings.TrimSpace(parsed.Hostname())
	if hostname == "" {
		return fmt.Errorf("url host is required")
	}
	if !allowLocal && (IsPrivateHost(hostname) || HasPrivateResolvedIP(hostname)) {
		return fmt.Errorf("access to private/local network addresses is disabled (url=%q)", rawURL)
	}
	return nil
}

// IsPathAllowed reports whether path is within one of the allowed root
// directories.  If allowedRoots is nil or empty, all paths are allowed.
func IsPathAllowed(path string, allowedRoots []string) bool {
	if len(allowedRoots) == 0 {
		return true
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	sep := string(filepath.Separator)
	for _, root := range allowedRoots {
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if abs == rootAbs || strings.HasPrefix(abs, rootAbs+sep) {
			return true
		}
	}
	return false
}

// Truncate shortens s to at most maxChars Unicode code points.
// If maxChars <= 0 the string is returned unchanged.
func Truncate(s string, maxChars int) string {
	if maxChars <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars]) + "\n[truncated]"
}
