package sandbox

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os/exec"
	"strings"
	"time"

	"metiq/internal/netpolicy"
)

type egressResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type sandboxEgressProxy struct {
	policy   netpolicy.NormalizedPolicy
	token    string
	listener net.Listener
	server   *http.Server
	resolver egressResolver
	dialer   func(context.Context, string, string) (net.Conn, error)
}

func startSandboxEgressProxy(cfg Config) (*sandboxEgressProxy, error) {
	policy, err := netpolicy.NormalizePolicy(netpolicy.Policy{
		AllowedDomains: cfg.AllowedDomains,
		AllowedCIDRs:   cfg.AllowedCIDRs,
	})
	if err != nil {
		return nil, fmt.Errorf("normalize sandbox egress policy: %w", err)
	}
	token, err := randomHex(32)
	if err != nil {
		return nil, fmt.Errorf("generate sandbox proxy credential: %w", err)
	}
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return nil, fmt.Errorf("listen for sandbox egress proxy: %w", err)
	}
	p := &sandboxEgressProxy{
		policy:   policy,
		token:    token,
		listener: listener,
		resolver: net.DefaultResolver,
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 15 * time.Second}
	p.dialer = dialer.DialContext
	p.server = &http.Server{
		Handler:           p,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go func() { _ = p.server.Serve(listener) }()
	return p, nil
}

func (p *sandboxEgressProxy) endpoint() string {
	port := p.listener.Addr().(*net.TCPAddr).Port
	return fmt.Sprintf("http://metiq:%s@host.docker.internal:%d", p.token, port)
}

func (p *sandboxEgressProxy) close() {
	if p == nil || p.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = p.server.Shutdown(ctx)
}

func (p *sandboxEgressProxy) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if !p.authenticated(req.Header.Get("Proxy-Authorization")) {
		w.Header().Set("Proxy-Authenticate", `Basic realm="metiq-sandbox"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if req.Method == http.MethodConnect {
		p.serveConnect(w, req)
		return
	}
	if req.URL == nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
		http.Error(w, "sandbox proxy supports only HTTP and HTTPS", http.StatusBadRequest)
		return
	}

	out := req.Clone(req.Context())
	out.RequestURI = ""
	out.Header = req.Header.Clone()
	out.Header.Del("Proxy-Authorization")
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           p.dialAuthorized,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	defer transport.CloseIdleConnections()
	resp, err := transport.RoundTrip(out)
	if err != nil {
		http.Error(w, "egress denied: "+err.Error(), http.StatusForbidden)
		return
	}
	defer resp.Body.Close()
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (p *sandboxEgressProxy) serveConnect(w http.ResponseWriter, req *http.Request) {
	upstream, err := p.dialAuthorized(req.Context(), "tcp", req.Host)
	if err != nil {
		http.Error(w, "egress denied: "+err.Error(), http.StatusForbidden)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		upstream.Close()
		http.Error(w, "proxy tunneling unsupported", http.StatusInternalServerError)
		return
	}
	client, _, err := hijacker.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	go proxyCopy(upstream, client)
	go proxyCopy(client, upstream)
}

func proxyCopy(dst, src net.Conn) {
	_, _ = io.Copy(dst, src)
	_ = dst.Close()
	_ = src.Close()
}

func (p *sandboxEgressProxy) authenticated(header string) bool {
	const prefix = "Basic "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(header, prefix)))
	if err != nil {
		return false
	}
	want := []byte("metiq:" + p.token)
	return len(decoded) == len(want) && subtle.ConstantTimeCompare(decoded, want) == 1
}

func (p *sandboxEgressProxy) dialAuthorized(ctx context.Context, network, address string) (net.Conn, error) {
	pinned, err := p.authorizeTarget(ctx, address)
	if err != nil {
		return nil, err
	}
	return p.dialer(ctx, network, pinned)
}

func (p *sandboxEgressProxy) authorizeTarget(ctx context.Context, address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("invalid egress target %q", address)
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" || port == "" {
		return "", fmt.Errorf("invalid egress target %q", address)
	}

	domainAllowed := p.policy.Allows(host)
	var addresses []netip.Addr
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
		addresses = []netip.Addr{ip.Unmap()}
	} else {
		addresses, err = p.resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return "", fmt.Errorf("resolve egress target %q: %w", host, err)
		}
	}
	if len(addresses) == 0 {
		return "", fmt.Errorf("egress target %q resolved to no addresses", host)
	}

	// Reject mixed/private answers rather than permitting DNS rebinding to a
	// daemon-local or RFC1918 service after policy evaluation.
	for _, address := range addresses {
		if netpolicy.IsBlockedIP(address.Unmap().String()) {
			return "", fmt.Errorf("egress target %q resolved to blocked address", host)
		}
	}
	for _, address := range addresses {
		address = address.Unmap()
		if domainAllowed || p.policy.Allows(address.String()) {
			return net.JoinHostPort(address.String(), port), nil
		}
	}
	return "", fmt.Errorf("egress target %q is not in the domain/CIDR allowlist", host)
}

func createInternalDockerNetwork(ctx context.Context) (string, func(), error) {
	suffix, err := randomHex(8)
	if err != nil {
		return "", nil, err
	}
	name := "metiq-egress-" + suffix
	if output, err := exec.CommandContext(ctx, "docker", "network", "create", "--driver=bridge", "--internal", name).CombinedOutput(); err != nil {
		return "", nil, fmt.Errorf("create internal sandbox network: %w: %s", err, strings.TrimSpace(string(output)))
	}
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = exec.CommandContext(cleanupCtx, "docker", "network", "rm", name).CombinedOutput()
	}
	return name, cleanup, nil
}

func randomHex(bytesLen int) (string, error) {
	data := make([]byte, bytesLen)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
