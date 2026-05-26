package channels

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SafeHTTPOptions controls SSRF-safe outbound fetch behavior for media.
type SafeHTTPOptions struct {
	AllowedHosts []string
	AllowHTTP    bool
	MaxBytes     int64
	Timeout      time.Duration
}

// MediaBlob is resolved media content and basic metadata.
type MediaBlob struct {
	URL  string
	MIME string
	Data []byte
}

// MediaResolver resolves a channel-specific media reference into bytes.
type MediaResolver interface {
	ResolveMedia(ctx context.Context, ref string) (MediaBlob, error)
}

func NewSSRFClient(opts SafeHTTPOptions) *http.Client {
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Disable environment proxies so destination validation cannot be bypassed by
	// CONNECT/proxy tunneling to internal hosts.
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = address
		}
		if err := validateSafeHost(ctx, host, opts.AllowedHosts); err != nil {
			return nil, err
		}
		return (&net.Dialer{Timeout: opts.Timeout}).DialContext(ctx, network, address)
	}
	client := &http.Client{Timeout: opts.Timeout, Transport: transport}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := ValidateSafeURL(req.URL.String(), opts); err != nil {
			return err
		}
		if len(via) > 0 && !sameOrigin(via[0].URL, req.URL) {
			req.Header.Del("Authorization")
			req.Header.Del("Cookie")
		}
		return nil
	}
	return client
}

func FetchSafe(ctx context.Context, rawURL string, headers map[string]string, opts SafeHTTPOptions) (MediaBlob, error) {
	if err := ValidateSafeURL(rawURL, opts); err != nil {
		return MediaBlob{}, err
	}
	client := NewSSRFClient(opts)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return MediaBlob{}, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return MediaBlob{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return MediaBlob{}, fmt.Errorf("safe fetch: status %d", resp.StatusCode)
	}
	max := opts.MaxBytes
	if max <= 0 {
		max = 25 << 20
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return MediaBlob{}, err
	}
	if int64(len(data)) > max {
		return MediaBlob{}, fmt.Errorf("safe fetch: body exceeds %d bytes", max)
	}
	return MediaBlob{URL: resp.Request.URL.String(), MIME: resp.Header.Get("Content-Type"), Data: data}, nil
}

func ValidateSafeURL(rawURL string, opts SafeHTTPOptions) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if u.Scheme != "https" && !(opts.AllowHTTP && u.Scheme == "http") {
		return fmt.Errorf("unsafe media URL scheme %q", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("unsafe media URL must not contain userinfo")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("unsafe media URL missing host")
	}
	return validateSafeHost(context.Background(), host, opts.AllowedHosts)
}

func validateSafeHost(ctx context.Context, host string, allowed []string) error {
	host = strings.Trim(strings.ToLower(host), ".")
	if host == "" {
		return fmt.Errorf("empty host")
	}
	if len(allowed) > 0 && !hostAllowed(host, allowed) {
		return fmt.Errorf("host %q not allowed", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return fmt.Errorf("host %q resolves to private address", host)
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		return fmt.Errorf("host %q has no addresses", host)
	}
	for _, addr := range ips {
		if !isPublicIP(addr.IP) {
			return fmt.Errorf("host %q resolves to private address %s", host, addr.IP)
		}
	}
	return nil
}

func hostAllowed(host string, allowed []string) bool {
	for _, item := range allowed {
		item = strings.Trim(strings.ToLower(item), ".")
		if item == "" {
			continue
		}
		if strings.HasPrefix(item, "*.") {
			suffix := strings.TrimPrefix(item, "*.")
			if host == suffix || strings.HasSuffix(host, "."+suffix) {
				return true
			}
			continue
		}
		if host == item {
			return true
		}
	}
	return false
}

func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 169 && ip4[1] == 254 {
			return false
		}
	}
	return true
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}
