package installer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RegistryPlugin describes a single plugin entry in a remote registry index.
type RegistryPlugin struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version,omitempty"`
	URL         string   `json:"url"`
	Type        string   `json:"type,omitempty"`
	Author      string   `json:"author,omitempty"`
	License     string   `json:"license,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type RegistryIndex struct {
	Version string           `json:"version"`
	Plugins []RegistryPlugin `json:"plugins"`
}

type DownloadOptions struct {
	Verifier            ArtifactVerifier
	RequireVerification bool
}

type DownloadResult struct {
	Path       string
	Provenance InstallProvenance
}

type ArtifactVerificationInput struct {
	Path       string
	Provenance InstallProvenance
}

type ArtifactVerifier interface {
	Verify(context.Context, ArtifactVerificationInput) (*ArtifactVerification, error)
}

type sha256ArtifactVerifier struct {
	expected string
}

// NewSHA256ArtifactVerifier returns a verifier for a caller-supplied artifact
// digest. Accepted forms are sha256:<hex>, sha256-<hex>, or raw 64-byte hex.
func NewSHA256ArtifactVerifier(expected string) (ArtifactVerifier, error) {
	normalized := strings.TrimSpace(expected)
	lower := strings.ToLower(normalized)
	switch {
	case strings.HasPrefix(lower, "sha256:"):
		normalized = strings.TrimSpace(normalized[len("sha256:"):])
	case strings.HasPrefix(lower, "sha256-"):
		normalized = strings.TrimSpace(normalized[len("sha256-"):])
	}
	if len(normalized) != sha256.Size*2 {
		return nil, fmt.Errorf("plugin artifact checksum must be a SHA-256 hex digest")
	}
	if _, err := hex.DecodeString(normalized); err != nil {
		return nil, fmt.Errorf("plugin artifact checksum must be valid hex: %w", err)
	}
	return sha256ArtifactVerifier{expected: strings.ToLower(normalized)}, nil
}

func (v sha256ArtifactVerifier) Verify(_ context.Context, input ArtifactVerificationInput) (*ArtifactVerification, error) {
	actual := strings.ToLower(strings.TrimSpace(input.Provenance.Artifact.Hash))
	if actual == "" || actual != v.expected {
		return nil, fmt.Errorf("SHA-256 mismatch: expected %s, got %s", v.expected, actual)
	}
	return &ArtifactVerification{Verifier: "sha256", Identity: "sha256:" + v.expected}, nil
}

var (
	installerHTTPClientMu  sync.RWMutex
	newInstallerHTTPClient = func(timeout time.Duration) *http.Client { return &http.Client{Timeout: timeout} }
	installerLookupIP      = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if literal := net.ParseIP(host); literal != nil {
			return []net.IPAddr{{IP: literal}}, nil
		}
		return net.DefaultResolver.LookupIPAddr(ctx, host)
	}
	installerDialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
)

func installerHTTPClient(timeout time.Duration) *http.Client {
	installerHTTPClientMu.RLock()
	factory := newInstallerHTTPClient
	installerHTTPClientMu.RUnlock()
	return factory(timeout)
}

type destinationGuard struct {
	mu      sync.Mutex
	pins    map[string][]net.IP
	records map[string]ResolvedHost
}

func newDestinationGuard() *destinationGuard {
	return &destinationGuard{pins: map[string][]net.IP{}, records: map[string]ResolvedHost{}}
}

func (g *destinationGuard) validateAndPin(ctx context.Context, u *url.URL) error {
	if err := validateParsedPluginURL(u); err != nil {
		return err
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	g.mu.Lock()
	_, ok := g.pins[host]
	g.mu.Unlock()
	if ok {
		return nil
	}
	addrs, err := installerLookupIP(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve plugin host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("resolve plugin host %q: no addresses", host)
	}
	seen := map[string]struct{}{}
	ips := make([]net.IP, 0, len(addrs))
	ipStrings := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		ip := addr.IP
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		}
		if !isPublicPluginIP(ip) {
			return fmt.Errorf("plugin URL host %q resolves to blocked address %s", host, ip)
		}
		s := ip.String()
		if _, exists := seen[s]; exists {
			continue
		}
		seen[s] = struct{}{}
		ips = append(ips, append(net.IP(nil), ip...))
		ipStrings = append(ipStrings, s)
	}
	sort.Strings(ipStrings)
	sort.Slice(ips, func(i, j int) bool { return ips[i].String() < ips[j].String() })
	g.mu.Lock()
	if _, exists := g.pins[host]; !exists {
		g.pins[host] = ips
		g.records[host] = ResolvedHost{Host: host, IPs: ipStrings}
	}
	g.mu.Unlock()
	return nil
}

func (g *destinationGuard) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid plugin destination %q: %w", address, err)
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	g.mu.Lock()
	ips := append([]net.IP(nil), g.pins[host]...)
	g.mu.Unlock()
	if len(ips) == 0 {
		return nil, fmt.Errorf("plugin destination %q was not validated", host)
	}
	var lastErr error
	for _, ip := range ips {
		conn, err := installerDialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("connect to pinned plugin host %q: %w", host, lastErr)
}

func (g *destinationGuard) provenance() []ResolvedHost {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]ResolvedHost, 0, len(g.records))
	for _, record := range g.records {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

func guardedHTTPClient(ctx context.Context, timeout time.Duration, initial *url.URL) (*http.Client, *destinationGuard, error) {
	guard := newDestinationGuard()
	if err := guard.validateAndPin(ctx, initial); err != nil {
		return nil, nil, err
	}
	base := installerHTTPClient(timeout)
	var transport *http.Transport
	switch t := base.Transport.(type) {
	case nil:
		transport = http.DefaultTransport.(*http.Transport).Clone()
	case *http.Transport:
		transport = t.Clone()
	default:
		return nil, nil, fmt.Errorf("plugin installer requires *http.Transport")
	}
	transport.Proxy = nil
	transport.DialContext = guard.dialContext
	transport.DialTLSContext = nil
	transport.DisableCompression = true
	client := &http.Client{Timeout: timeout, Transport: transport}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many plugin download redirects")
		}
		if err := guard.validateAndPin(req.Context(), req.URL); err != nil {
			return fmt.Errorf("unsafe plugin redirect: %w", err)
		}
		if len(via) > 0 && !sameOrigin(via[len(via)-1].URL, req.URL) {
			req.Header.Del("Authorization")
			req.Header.Del("Proxy-Authorization")
			req.Header.Del("Cookie")
		}
		return nil
	}
	return client, guard, nil
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func parsePluginURL(rawURL string) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("URL is required")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	if err := validateParsedPluginURL(u); err != nil {
		return nil, err
	}
	u.Fragment = ""
	return u, nil
}

func validatePluginURL(rawURL string) error {
	_, err := parsePluginURL(rawURL)
	return err
}

func validateParsedPluginURL(u *url.URL) error {
	if u == nil {
		return fmt.Errorf("URL is required")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("only https:// URLs are supported for plugin downloads (got %q)", u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("URL %q has no host", u.String())
	}
	if u.User != nil {
		return fmt.Errorf("plugin URLs must not contain userinfo")
	}
	if strings.Contains(u.Hostname(), "%") {
		return fmt.Errorf("plugin URL IPv6 zones are not allowed")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("plugin URL has invalid port %q", port)
		}
	}
	return nil
}

func isPublicPluginIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	blocked := []string{
		"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"2001:db8::/32", "2001:2::/48", "2001::/32", "2002::/16", "64:ff9b::/96", "100::/64",
	}
	for _, raw := range blocked {
		_, network, _ := net.ParseCIDR(raw)
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func DownloadURL(ctx context.Context, rawURL string) (string, error) {
	result, err := DownloadURLWithOptions(ctx, rawURL, DownloadOptions{})
	return result.Path, err
}

func DownloadURLWithOptions(ctx context.Context, rawURL string, options DownloadOptions) (DownloadResult, error) {
	if options.RequireVerification && options.Verifier == nil {
		return DownloadResult{}, fmt.Errorf("plugin artifact verifier is required")
	}
	u, err := parsePluginURL(rawURL)
	if err != nil {
		return DownloadResult{}, err
	}
	client, guard, err := guardedHTTPClient(ctx, 5*time.Minute, u)
	if err != nil {
		return DownloadResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", "metiq-plugin-installer/1.0")
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := client.Do(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return DownloadResult{}, fmt.Errorf("download %s: %w", u.String(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return DownloadResult{}, fmt.Errorf("download %s: HTTP %d", u.String(), resp.StatusCode)
	}
	const maxBytes = 50 << 20
	if resp.ContentLength > maxBytes {
		return DownloadResult{}, fmt.Errorf("download exceeded maximum size of %d bytes", maxBytes)
	}
	ext := downloadExtension(resp.Request.URL)
	tmp, err := os.CreateTemp("", "metiq-plugin-*"+ext)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("create temp file: %w", err)
	}
	path := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(path)
		}
	}()
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxBytes+1))
	if copyErr != nil {
		_ = tmp.Close()
		return DownloadResult{}, fmt.Errorf("write download: %w", copyErr)
	}
	if n > maxBytes {
		_ = tmp.Close()
		return DownloadResult{}, fmt.Errorf("download exceeded maximum size of %d bytes", maxBytes)
	}
	if err := tmp.Close(); err != nil {
		return DownloadResult{}, fmt.Errorf("close download: %w", err)
	}
	provenance := InstallProvenance{
		SourceURL: u.String(), FinalURL: resp.Request.URL.String(), ResolvedHosts: guard.provenance(),
		Artifact: ArtifactDigest{Algorithm: "sha256", Hash: hex.EncodeToString(h.Sum(nil)), SizeBytes: n},
	}
	if options.Verifier != nil {
		verification, err := options.Verifier.Verify(ctx, ArtifactVerificationInput{Path: path, Provenance: provenance})
		if err != nil {
			return DownloadResult{}, fmt.Errorf("verify plugin artifact: %w", err)
		}
		if verification == nil || strings.TrimSpace(verification.Verifier) == "" {
			return DownloadResult{}, fmt.Errorf("verify plugin artifact: verifier returned no identity")
		}
		provenance.Verification = verification
	}
	ok = true
	return DownloadResult{Path: path, Provenance: provenance}, nil
}

func downloadExtension(u *url.URL) string {
	base := filepath.Base(u.Path)
	switch {
	case strings.HasSuffix(base, ".tar.gz"), strings.HasSuffix(base, ".tgz"):
		return ".tar.gz"
	case strings.HasSuffix(base, ".zip"):
		return ".zip"
	case strings.HasSuffix(base, ".js"):
		return ".js"
	default:
		return ""
	}
}

func FetchRegistry(ctx context.Context, registryURL string) (*RegistryIndex, error) {
	u, err := parsePluginURL(registryURL)
	if err != nil {
		return nil, err
	}
	client, _, err := guardedHTTPClient(ctx, 30*time.Second, u)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create registry request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "metiq-plugin-installer/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch registry %s: %w", u.String(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch registry %s: HTTP %d", u.String(), resp.StatusCode)
	}
	var index RegistryIndex
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&index); err != nil {
		return nil, fmt.Errorf("parse registry index: %w", err)
	}
	if len(index.Plugins) == 0 && index.Version == "" {
		return nil, fmt.Errorf("registry response does not look like a valid index")
	}
	return &index, nil
}
