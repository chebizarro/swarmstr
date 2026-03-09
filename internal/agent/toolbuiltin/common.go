// Package toolbuiltin provides built-in agent tool implementations that are
// registered at daemon startup.  Each exported function returns an
// agent.ToolFunc that can be passed directly to agent.ToolRegistry.Register.
package toolbuiltin

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"time"
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
	private := []string{
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16", // link-local
		"::1/128",
		"fc00::/7",  // IPv6 ULA
		"fe80::/10", // IPv6 link-local
	}
	for _, cidr := range private {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	private := []string{
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16", // link-local
		"::1/128",
		"fc00::/7",  // IPv6 ULA
		"fe80::/10", // IPv6 link-local
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

// ValidateFetchURL checks that rawURL is acceptable as a web-fetch target.
// It rejects empty URLs, non-http/https schemes, and (unless allowLocal is
// true) private-network hosts as a lightweight SSRF guard.
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
