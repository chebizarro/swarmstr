package toolgrpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"metiq/internal/config"
	"metiq/internal/secrets"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const (
	grpcTransportTLSModeInsecurePlaintext = "insecure_plaintext"
	MaxCredentialSourceBytes              = 1 << 20
)

// CredentialSource identifies an operator-controlled metadata credential.
// Ref accepts secret:/env:/$ references or an absolute file: path. Encoding is
// text or hex; hex encodes the resolved bytes as lowercase metadata text.
type CredentialSource struct {
	Ref      string `json:"ref" yaml:"ref"`
	Encoding string `json:"encoding,omitempty" yaml:"encoding,omitempty"`
}

// ValueResolver resolves credential bytes without exposing secret material.
// Implementations are called for every RPC so rotated macaroons take effect
// without rebuilding a provider.
type ValueResolver interface {
	ResolveBytes(context.Context, CredentialSource) ([]byte, error)
}

// ValueResolverFunc adapts a function to ValueResolver.
type ValueResolverFunc func(context.Context, CredentialSource) ([]byte, error)

func (f ValueResolverFunc) ResolveBytes(ctx context.Context, source CredentialSource) ([]byte, error) {
	return f(ctx, source)
}

// ConnectionManagerOption customizes credential resolution for a manager.
type ConnectionManagerOption func(*connectionManagerOptions)

type connectionManagerOptions struct {
	resolver        ValueResolver
	metadataSources map[string]map[string]CredentialSource
}

// WithValueResolver injects the daemon-owned resolver used for typed metadata
// sources. A nil resolver leaves the compatibility resolver in place.
func WithValueResolver(resolver ValueResolver) ConnectionManagerOption {
	return func(opts *connectionManagerOptions) {
		if resolver != nil {
			opts.resolver = resolver
		}
	}
}

// WithMetadataSources binds metadata keys to credential sources by profile ID.
func WithMetadataSources(sources map[string]map[string]CredentialSource) ConnectionManagerOption {
	return func(opts *connectionManagerOptions) {
		opts.metadataSources = cloneMetadataSources(sources)
	}
}

type contextKey string

const policyAppliedContextKey contextKey = "toolgrpc.policy_applied"

// CallOptions contains per-call policy inputs accepted by the gRPC connection
// manager. Metadata keys must be listed in the endpoint profile's
// auth.allow_override_keys before they can be supplied here.
type CallOptions struct {
	Metadata   map[string]string
	DeadlineMS int
}

// ConnectionManager owns shared gRPC ClientConn instances for configured
// endpoint profiles. Connections are opened lazily and reused until Close or
// CloseProfile is called.
type ConnectionManager struct {
	mu              sync.Mutex
	profiles        map[string]config.GRPCEndpointConfig
	conns           map[string]*grpc.ClientConn
	dialLocks       map[string]*sync.Mutex
	resolver        ValueResolver
	metadataSources map[string]map[string]CredentialSource
}

var grpcDialContext = grpc.DialContext

// NewConnectionManager builds a connection pool for the supplied endpoint
// profiles. Endpoint IDs are matched case-sensitively after trimming spaces.
func NewConnectionManager(endpoints []config.GRPCEndpointConfig) (*ConnectionManager, error) {
	return NewConnectionManagerWithOptions(endpoints)
}

// NewConnectionManagerWithOptions builds a connection pool with additive
// credential resolution options.
func NewConnectionManagerWithOptions(endpoints []config.GRPCEndpointConfig, managerOpts ...ConnectionManagerOption) (*ConnectionManager, error) {
	opts := connectionManagerOptions{resolver: newDefaultValueResolver()}
	for _, apply := range managerOpts {
		if apply != nil {
			apply(&opts)
		}
	}
	profiles := make(map[string]config.GRPCEndpointConfig, len(endpoints))
	for i, endpoint := range endpoints {
		id := strings.TrimSpace(endpoint.ID)
		if id == "" {
			return nil, fmt.Errorf("grpc endpoint %d: id is required", i)
		}
		if strings.TrimSpace(endpoint.Target) == "" {
			return nil, fmt.Errorf("grpc endpoint %q: target is required", id)
		}
		if _, exists := profiles[id]; exists {
			return nil, fmt.Errorf("grpc endpoint %q: duplicate id", id)
		}
		endpoint.ID = id
		profiles[id] = endpoint
	}
	dialLocks := make(map[string]*sync.Mutex, len(profiles))
	for id := range profiles {
		dialLocks[id] = &sync.Mutex{}
	}
	return &ConnectionManager{
		profiles:        profiles,
		conns:           make(map[string]*grpc.ClientConn, len(profiles)),
		dialLocks:       dialLocks,
		resolver:        opts.resolver,
		metadataSources: opts.metadataSources,
	}, nil
}

// NewConnectionManagerFromConfig builds a connection manager from the typed
// gRPC config model.
func NewConnectionManagerFromConfig(cfg config.GRPCConfig) (*ConnectionManager, error) {
	return NewConnectionManager(cfg.Endpoints)
}

// Profile returns the endpoint profile for id when it exists.
func (m *ConnectionManager) Profile(id string) (config.GRPCEndpointConfig, bool) {
	if m == nil {
		return config.GRPCEndpointConfig{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	profile, ok := m.profiles[strings.TrimSpace(id)]
	return profile, ok
}

// Conn returns the shared ClientConn for a profile, dialing it on first use.
func (m *ConnectionManager) Conn(ctx context.Context, profileID string) (*grpc.ClientConn, error) {
	if m == nil {
		return nil, errors.New("grpc connection manager is nil")
	}
	id := strings.TrimSpace(profileID)
	m.mu.Lock()
	if conn := m.conns[id]; conn != nil {
		m.mu.Unlock()
		return conn, nil
	}
	profile, ok := m.profiles[id]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("grpc profile %q is not configured", profileID)
	}
	dialLock := m.dialLocks[id]
	if dialLock == nil {
		dialLock = &sync.Mutex{}
		m.dialLocks[id] = dialLock
	}
	m.mu.Unlock()

	dialLock.Lock()
	defer dialLock.Unlock()

	m.mu.Lock()
	if conn := m.conns[id]; conn != nil {
		m.mu.Unlock()
		return conn, nil
	}
	profile = m.profiles[id]
	m.mu.Unlock()

	conn, err := dialProfileWithPolicy(ctx, profile, m.resolver, m.metadataSources[id])
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if existing := m.conns[id]; existing != nil {
		m.mu.Unlock()
		_ = conn.Close()
		return existing, nil
	}
	m.conns[id] = conn
	m.mu.Unlock()
	return conn, nil
}

// CallContext applies a profile's default auth metadata and deadline policy to
// ctx, plus allowed per-call metadata/deadline overrides. The returned cancel
// function must be called by the caller when the RPC is complete.
func (m *ConnectionManager) CallContext(ctx context.Context, profileID string, opts CallOptions) (context.Context, context.CancelFunc, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if m == nil {
		return nil, nil, errors.New("grpc connection manager is nil")
	}
	profile, ok := m.Profile(profileID)
	if !ok {
		return nil, nil, fmt.Errorf("grpc profile %q is not configured", profileID)
	}
	return applyCallPolicyWithResolver(ctx, profile, opts, m.resolver, m.metadataSources[strings.TrimSpace(profileID)])
}

// InvokeUnary applies connection-manager policy and invokes a unary RPC through
// the pooled connection for profileID.
func (m *ConnectionManager) InvokeUnary(ctx context.Context, profileID, method string, request any, response any, opts CallOptions, callOpts ...grpc.CallOption) error {
	conn, err := m.Conn(ctx, profileID)
	if err != nil {
		return err
	}
	callCtx, cancel, err := m.CallContext(ctx, profileID, opts)
	if err != nil {
		return err
	}
	defer cancel()
	return conn.Invoke(callCtx, method, request, response, callOpts...)
}

// CloseProfile closes and removes the pooled connection for one profile.
func (m *ConnectionManager) CloseProfile(profileID string) error {
	if m == nil {
		return nil
	}
	id := strings.TrimSpace(profileID)
	m.mu.Lock()
	conn := m.conns[id]
	delete(m.conns, id)
	m.mu.Unlock()
	if conn == nil {
		return nil
	}
	return conn.Close()
}

// Close closes all pooled connections. The manager may be reused after Close;
// subsequent Conn calls will dial fresh connections.
func (m *ConnectionManager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	conns := make([]*grpc.ClientConn, 0, len(m.conns))
	for id, conn := range m.conns {
		conns = append(conns, conn)
		delete(m.conns, id)
	}
	m.mu.Unlock()

	var joined error
	for _, conn := range conns {
		if err := conn.Close(); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func dialProfile(ctx context.Context, profile config.GRPCEndpointConfig) (*grpc.ClientConn, error) {
	return dialProfileWithPolicy(ctx, profile, newDefaultValueResolver(), nil)
}

func dialProfileWithPolicy(ctx context.Context, profile config.GRPCEndpointConfig, resolver ValueResolver, sources map[string]CredentialSource) (*grpc.ClientConn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	dialTimeout := time.Duration(profile.Defaults.EffectiveDialTimeoutMS()) * time.Millisecond
	if dialTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, dialTimeout)
		defer cancel()
	}

	dialOpts, err := buildDialOptionsWithPolicy(profile, resolver, sources)
	if err != nil {
		return nil, err
	}
	conn, err := grpcDialContext(ctx, strings.TrimSpace(profile.Target), dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("dial grpc profile %q (%s): %w", profile.ID, profile.Target, err)
	}
	return conn, nil
}

func buildDialOptions(profile config.GRPCEndpointConfig) ([]grpc.DialOption, error) {
	return buildDialOptionsWithPolicy(profile, newDefaultValueResolver(), nil)
}

func buildDialOptionsWithPolicy(profile config.GRPCEndpointConfig, resolver ValueResolver, sources map[string]CredentialSource) ([]grpc.DialOption, error) {
	creds, err := buildTransportCredentials(profile.Transport)
	if err != nil {
		return nil, fmt.Errorf("grpc profile %q transport: %w", profile.ID, err)
	}
	maxRecv := profile.Defaults.EffectiveMaxRecvMessageBytes()
	if maxRecv <= 0 {
		maxRecv = config.DefaultGRPCMaxRecvMessageBytes
	}
	return []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxRecv)),
		grpc.WithUnaryInterceptor(unaryPolicyInterceptor(profile, resolver, sources)),
		grpc.WithStreamInterceptor(streamPolicyInterceptor(profile, resolver, sources)),
	}, nil
}

func buildTransportCredentials(transport config.GRPCTransportConfig) (credentials.TransportCredentials, error) {
	mode := strings.ToLower(strings.TrimSpace(transport.TLSMode))
	if mode == "" {
		mode = config.GRPCTransportTLSModeSystem
	}
	switch mode {
	case config.GRPCTransportTLSModeInsecure, grpcTransportTLSModeInsecurePlaintext:
		return insecure.NewCredentials(), nil
	case config.GRPCTransportTLSModeSystem:
		return credentials.NewTLS(&tls.Config{ServerName: strings.TrimSpace(transport.ServerName)}), nil
	case config.GRPCTransportTLSModeCustomCA:
		pool, err := certPoolFromFile(transport.CAFile)
		if err != nil {
			return nil, err
		}
		return credentials.NewTLS(&tls.Config{RootCAs: pool, ServerName: strings.TrimSpace(transport.ServerName)}), nil
	case config.GRPCTransportTLSModeMTLS:
		cert, err := tls.LoadX509KeyPair(strings.TrimSpace(transport.CertFile), strings.TrimSpace(transport.KeyFile))
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		var pool *x509.CertPool
		if strings.TrimSpace(transport.CAFile) != "" {
			pool, err = certPoolFromFile(transport.CAFile)
			if err != nil {
				return nil, err
			}
		}
		return credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      pool,
			ServerName:   strings.TrimSpace(transport.ServerName),
		}), nil
	default:
		return nil, fmt.Errorf("unknown tls_mode %q", transport.TLSMode)
	}
}

func certPoolFromFile(path string) (*x509.CertPool, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, errors.New("ca_file is required")
	}
	pemBytes, err := os.ReadFile(trimmed)
	if err != nil {
		return nil, fmt.Errorf("read ca_file %q: %w", trimmed, err)
	}
	pool := x509.NewCertPool()
	if ok := pool.AppendCertsFromPEM(pemBytes); !ok {
		return nil, fmt.Errorf("ca_file %q did not contain any PEM certificates", trimmed)
	}
	return pool, nil
}

func unaryPolicyInterceptor(profile config.GRPCEndpointConfig, resolver ValueResolver, sources map[string]CredentialSource) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req any, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		var cancel context.CancelFunc
		var err error
		ctx, cancel, err = ensureCallPolicy(ctx, profile, resolver, sources)
		if err != nil {
			return err
		}
		defer cancel()
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func streamPolicyInterceptor(profile config.GRPCEndpointConfig, resolver ValueResolver, sources map[string]CredentialSource) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		ctx, cancel, err := ensureCallPolicy(ctx, profile, resolver, sources)
		if err != nil {
			return nil, err
		}
		stream, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil {
			cancel()
			return nil, err
		}
		return &cancelableStream{ClientStream: stream, cancel: cancel}, nil
	}
}

type cancelableStream struct {
	grpc.ClientStream
	cancel context.CancelFunc
	once   sync.Once
}

func (s *cancelableStream) CloseSend() error {
	err := s.ClientStream.CloseSend()
	if err != nil {
		s.cancelOnce()
	}
	return err
}

func (s *cancelableStream) SendMsg(m any) error {
	err := s.ClientStream.SendMsg(m)
	if err != nil {
		s.cancelOnce()
	}
	return err
}

func (s *cancelableStream) RecvMsg(m any) error {
	err := s.ClientStream.RecvMsg(m)
	if err != nil {
		s.cancelOnce()
	}
	return err
}

func (s *cancelableStream) cancelOnce() {
	if s.cancel == nil {
		return
	}
	s.once.Do(s.cancel)
}

func ensureCallPolicy(ctx context.Context, profile config.GRPCEndpointConfig, resolver ValueResolver, sources map[string]CredentialSource) (context.Context, context.CancelFunc, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if applied, _ := ctx.Value(policyAppliedContextKey).(bool); applied {
		return ctx, func() {}, nil
	}
	return applyCallPolicyWithResolver(ctx, profile, CallOptions{}, resolver, sources)
}

func applyCallPolicy(ctx context.Context, profile config.GRPCEndpointConfig, opts CallOptions) (context.Context, context.CancelFunc, error) {
	return applyCallPolicyWithResolver(ctx, profile, opts, newDefaultValueResolver(), nil)
}

func applyCallPolicyWithResolver(ctx context.Context, profile config.GRPCEndpointConfig, opts CallOptions, resolver ValueResolver, sources map[string]CredentialSource) (context.Context, context.CancelFunc, error) {
	merged, err := mergedOutgoingMetadata(ctx, profile, opts.Metadata, resolver, sources)
	if err != nil {
		return nil, nil, err
	}
	deadlineMS, err := effectiveDeadlineMS(profile, opts.DeadlineMS)
	if err != nil {
		return nil, nil, err
	}
	ctx = metadata.NewOutgoingContext(ctx, merged)
	deadlineDuration, err := deadlineDurationFromMS(deadlineMS)
	if err != nil {
		return nil, nil, fmt.Errorf("grpc profile %q deadline_ms %d is invalid: %w", profile.ID, deadlineMS, err)
	}
	ctx, cancel := context.WithTimeout(ctx, deadlineDuration)
	ctx = context.WithValue(ctx, policyAppliedContextKey, true)
	return ctx, cancel, nil
}

func mergedOutgoingMetadata(ctx context.Context, profile config.GRPCEndpointConfig, overrides map[string]string, resolver ValueResolver, sources map[string]CredentialSource) (metadata.MD, error) {
	allowed := allowedOverrideKeys(profile.Auth.AllowOverrideKeys)
	merged := metadata.New(nil)
	for key, source := range sources {
		canonical, err := normalizeMetadataKey(key)
		if err != nil {
			return nil, fmt.Errorf("grpc profile %q credential metadata %q: %w", profile.ID, key, err)
		}
		if _, duplicate := profile.Auth.Metadata[key]; duplicate {
			return nil, fmt.Errorf("grpc profile %q metadata %q has both static and credential sources", profile.ID, canonical)
		}
		resolved, err := resolveCredentialMetadata(ctx, resolver, source)
		if err != nil {
			return nil, fmt.Errorf("grpc profile %q credential metadata %q: %w", profile.ID, canonical, err)
		}
		merged.Set(canonical, resolved)
	}
	for key, value := range profile.Auth.Metadata {
		canonical, err := normalizeMetadataKey(key)
		if err != nil {
			return nil, fmt.Errorf("grpc profile %q auth metadata %q: %w", profile.ID, key, err)
		}
		resolved, err := resolveMetadataSecretValue(ctx, resolver, value)
		if err != nil {
			return nil, fmt.Errorf("grpc profile %q auth metadata %q: %w", profile.ID, key, err)
		}
		merged.Set(canonical, resolved)
	}
	if existing, ok := metadata.FromOutgoingContext(ctx); ok {
		for key, values := range existing {
			canonical, err := normalizeMetadataKey(key)
			if err != nil {
				return nil, fmt.Errorf("grpc profile %q outgoing metadata %q: %w", profile.ID, key, err)
			}
			if !allowed[canonical] {
				return nil, fmt.Errorf("grpc profile %q metadata override %q is not allowed", profile.ID, canonical)
			}
			merged.Set(canonical, values...)
		}
	}
	for key, value := range overrides {
		canonical, err := normalizeMetadataKey(key)
		if err != nil {
			return nil, fmt.Errorf("grpc profile %q call metadata %q: %w", profile.ID, key, err)
		}
		if !allowed[canonical] {
			return nil, fmt.Errorf("grpc profile %q metadata override %q is not allowed", profile.ID, canonical)
		}
		resolved, err := resolveMetadataSecretValue(ctx, resolver, value)
		if err != nil {
			return nil, fmt.Errorf("grpc profile %q call metadata %q: %w", profile.ID, key, err)
		}
		merged.Set(canonical, resolved)
	}
	return merged, nil
}

func allowedOverrideKeys(keys []string) map[string]bool {
	allowed := make(map[string]bool, len(keys))
	for _, key := range keys {
		if canonical, err := normalizeMetadataKey(key); err == nil {
			allowed[canonical] = true
		}
	}
	return allowed
}

func normalizeMetadataKey(key string) (string, error) {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return "", errors.New("metadata key is required")
	}
	if trimmed != strings.ToLower(trimmed) {
		return "", errors.New("metadata key must be lowercase")
	}
	if strings.HasPrefix(trimmed, ":") {
		return "", errors.New("pseudo-header metadata keys are not configurable")
	}
	if strings.HasPrefix(trimmed, "grpc-") {
		return "", errors.New("metadata key must not use reserved grpc- prefix")
	}
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return "", fmt.Errorf("metadata key contains invalid character %q", r)
	}
	return trimmed, nil
}

func deadlineDurationFromMS(deadlineMS int) (time.Duration, error) {
	if deadlineMS < 0 {
		return 0, errors.New("deadline_ms must be >= 0")
	}
	maxDeadlineMS := int64(math.MaxInt64 / int64(time.Millisecond))
	if int64(deadlineMS) > maxDeadlineMS {
		return 0, fmt.Errorf("deadline_ms exceeds maximum supported value %d", maxDeadlineMS)
	}
	return time.Duration(deadlineMS) * time.Millisecond, nil
}

func effectiveDeadlineMS(profile config.GRPCEndpointConfig, requestedMS int) (int, error) {
	if requestedMS < 0 {
		return 0, fmt.Errorf("grpc profile %q deadline_ms must be >= 0", profile.ID)
	}
	maxMS := profile.Defaults.EffectiveMaxDeadlineMS()
	if maxMS <= 0 {
		maxMS = config.DefaultGRPCMaxDeadlineMS
	}
	deadlineMS := profile.Defaults.EffectiveDeadlineMS()
	if requestedMS > 0 {
		deadlineMS = requestedMS
	}
	if deadlineMS <= 0 {
		deadlineMS = config.DefaultGRPCDeadlineMS
	}
	if deadlineMS > maxMS {
		return 0, fmt.Errorf("grpc profile %q deadline_ms %d exceeds max_deadline_ms %d", profile.ID, deadlineMS, maxMS)
	}
	return deadlineMS, nil
}

type defaultValueResolver struct {
	mu    sync.Mutex
	store *secrets.Store
}

func newDefaultValueResolver() *defaultValueResolver {
	return &defaultValueResolver{}
}

func (r *defaultValueResolver) ResolveBytes(ctx context.Context, source CredentialSource) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ref := strings.TrimSpace(source.Ref)
	if ref == "" {
		return nil, errors.New("credential reference is required")
	}
	var raw []byte
	switch {
	case strings.HasPrefix(ref, "file:"):
		path := strings.TrimSpace(strings.TrimPrefix(ref, "file:"))
		if path == "" || !filepath.IsAbs(path) {
			return nil, errors.New("credential file path must be absolute")
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, errors.New("credential file is unavailable")
		}
		defer file.Close()
		raw, err = io.ReadAll(io.LimitReader(file, MaxCredentialSourceBytes+1))
		if err != nil {
			return nil, errors.New("credential file could not be read")
		}
		if len(raw) > MaxCredentialSourceBytes {
			return nil, fmt.Errorf("credential exceeds %d byte limit", MaxCredentialSourceBytes)
		}
	case strings.HasPrefix(ref, "secret:"), strings.HasPrefix(ref, "env:"), strings.HasPrefix(ref, "${"), strings.HasPrefix(ref, "$"):
		lookup := ref
		if strings.HasPrefix(ref, "secret:") {
			name := strings.TrimSpace(strings.TrimPrefix(ref, "secret:"))
			if name == "" {
				return nil, errors.New("credential secret name is required")
			}
			lookup = "env:" + name
		}
		r.mu.Lock()
		if r.store == nil {
			r.store = secrets.NewStore(nil)
		}
		_, _ = r.store.Reload()
		value, found := r.store.Resolve(lookup)
		r.mu.Unlock()
		if !found {
			return nil, errors.New("credential secret was not found")
		}
		raw = []byte(value)
	default:
		return nil, errors.New("unsupported credential reference scheme")
	}
	if len(raw) == 0 {
		return nil, errors.New("credential resolved to an empty value")
	}
	switch strings.ToLower(strings.TrimSpace(source.Encoding)) {
	case "", "text":
		for _, b := range raw {
			if b < 0x20 || b > 0x7e {
				return nil, errors.New("text credential contains non-ASCII bytes")
			}
		}
		return raw, nil
	case "hex":
		return []byte(hex.EncodeToString(raw)), nil
	default:
		return nil, errors.New("credential encoding must be text or hex")
	}
}

func resolveCredentialMetadata(ctx context.Context, resolver ValueResolver, source CredentialSource) (string, error) {
	if resolver == nil {
		return "", errors.New("credential resolver is unavailable")
	}
	resolved, err := resolver.ResolveBytes(ctx, source)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", errors.New("credential resolution failed")
	}
	if len(resolved) == 0 {
		return "", errors.New("credential resolved to an empty value")
	}
	encoding := strings.ToLower(strings.TrimSpace(source.Encoding))
	maxResolved := MaxCredentialSourceBytes
	if encoding == "hex" {
		maxResolved *= 2
	}
	if len(resolved) > maxResolved {
		return "", fmt.Errorf("encoded credential exceeds %d byte limit", maxResolved)
	}
	for _, b := range resolved {
		if b < 0x20 || b > 0x7e {
			return "", errors.New("credential contains non-ASCII metadata bytes")
		}
		if encoding == "hex" && !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'f')) {
			return "", errors.New("hex credential resolver returned non-lowercase-hex metadata")
		}
	}
	if encoding == "hex" && len(resolved)%2 != 0 {
		return "", errors.New("hex credential resolver returned an odd-length value")
	}
	return string(resolved), nil
}

func resolveMetadataSecretValue(ctx context.Context, resolver ValueResolver, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "secret:") {
		return value, nil
	}
	return resolveCredentialMetadata(ctx, resolver, CredentialSource{Ref: trimmed, Encoding: "text"})
}

func cloneMetadataSources(in map[string]map[string]CredentialSource) map[string]map[string]CredentialSource {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]map[string]CredentialSource, len(in))
	for profileID, sources := range in {
		cloned := make(map[string]CredentialSource, len(sources))
		for key, source := range sources {
			cloned[key] = source
		}
		out[profileID] = cloned
	}
	return out
}
