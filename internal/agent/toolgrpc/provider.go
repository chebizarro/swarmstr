package toolgrpc

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent"
	"metiq/internal/config"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/descriptorpb"
)

const defaultStreamManagerIdleTTL = 5 * time.Minute

// EndpointSource combines a runtime endpoint profile with an optional bundled
// descriptor set and curated full-RPC overrides. A non-nil ToolNames map is an
// exact allowlist; methods absent from it are not exposed.
type EndpointSource struct {
	Profile          config.GRPCEndpointConfig
	DescriptorSet    *descriptorpb.FileDescriptorSet
	ToolNames        map[string]string
	ToolTraits       map[string]agent.ToolTraits
	ToolDescriptions map[string]string
	MetadataSources  map[string]CredentialSource
}

// EndpointSourcesFromConfig preserves generic gRPC behavior by translating
// configured endpoints into sources without embedded descriptors or overrides.
func EndpointSourcesFromConfig(cfg config.GRPCConfig) []EndpointSource {
	out := make([]EndpointSource, 0, len(cfg.Endpoints))
	for _, profile := range cfg.Endpoints {
		out = append(out, EndpointSource{Profile: profile})
	}
	return out
}

// Provider discovers configured gRPC endpoint profiles and exposes their methods
// as standard agent.ToolRegistration values for the shared tool registry.
type Provider struct {
	manager  *ConnectionManager
	methods  []MethodSpec
	regs     []agent.ToolRegistration
	redactor Redactor

	mu             sync.Mutex
	streamManagers map[string]*StreamManager
	closing        bool
	inFlight       int
	closeCh        chan struct{}
	closeChOnce    sync.Once
	closeOnce      sync.Once
	closeErr       error

	streamManagerIdleTTL time.Duration
}

// NewProvider discovers all configured endpoint profiles and prepares tool
// registrations. The provider does not mutate the caller's registry until
// RegisterInto is called.
func NewProvider(ctx context.Context, cfg config.GRPCConfig) (*Provider, error) {
	if errs := cfg.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("invalid grpc config: %w", errors.Join(errs...))
	}
	return NewProviderFromSources(ctx, EndpointSourcesFromConfig(cfg))
}

// NewProviderFromSources discovers generic and bundled endpoint sources without
// requiring protoc, filesystem descriptor paths, or server reflection.
func NewProviderFromSources(ctx context.Context, sources []EndpointSource, managerOpts ...ConnectionManagerOption) (*Provider, error) {
	if len(sources) == 0 {
		return newProvider(nil), nil
	}
	profiles := make([]config.GRPCEndpointConfig, 0, len(sources))
	metadataSources := make(map[string]map[string]CredentialSource)
	seenProfiles := make(map[string]struct{}, len(sources))
	for i, source := range sources {
		profile := source.Profile
		id := strings.TrimSpace(profile.ID)
		if id == "" {
			return nil, fmt.Errorf("grpc endpoint source %d: profile id is required", i)
		}
		if strings.TrimSpace(profile.Target) == "" {
			return nil, fmt.Errorf("grpc endpoint source %q: target is required", id)
		}
		if _, duplicate := seenProfiles[id]; duplicate {
			return nil, fmt.Errorf("grpc endpoint source %q: duplicate profile id", id)
		}
		seenProfiles[id] = struct{}{}
		if source.DescriptorSet != nil && len(source.DescriptorSet.File) == 0 {
			return nil, fmt.Errorf("grpc endpoint source %q: embedded descriptor set is empty", id)
		}
		profile.ID = id
		sources[i].Profile = profile
		profiles = append(profiles, profile)
		if len(source.MetadataSources) > 0 {
			metadataSources[id] = source.MetadataSources
		}
	}
	managerOpts = append(managerOpts, WithMetadataSources(metadataSources))
	manager, err := NewConnectionManagerWithOptions(profiles, managerOpts...)
	if err != nil {
		return nil, err
	}
	p := newProvider(manager)
	for _, source := range sources {
		methods, err := p.discoverSource(ctx, source)
		if err != nil {
			_ = manager.Close()
			return nil, err
		}
		p.methods = append(p.methods, methods...)
	}
	if err := p.assignGlobalToolNames(); err != nil {
		_ = manager.Close()
		return nil, err
	}
	p.regs = p.buildRegistrations(profiles)
	return p, nil
}

func newProvider(manager *ConnectionManager) *Provider {
	return &Provider{
		manager:              manager,
		redactor:             NewRedactor(),
		streamManagers:       map[string]*StreamManager{},
		closeCh:              make(chan struct{}),
		streamManagerIdleTTL: defaultStreamManagerIdleTTL,
	}
}

// RegisterInto adds all discovered gRPC tools to the shared registry. Using the
// main registry ensures schema validation, semantic validation, hooks, traces,
// lifecycle events, and profile filtering flow through the same path as other
// tools.
func (p *Provider) RegisterInto(registry *agent.ToolRegistry) {
	if p == nil || registry == nil {
		return
	}
	for _, reg := range p.regs {
		name := reg.Descriptor.Name
		if name == "" {
			continue
		}
		registry.RegisterTool(name, reg)
	}
}

func (p *Provider) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if !p.closing {
		p.closing = true
		if p.inFlight == 0 {
			p.signalClosed()
		}
	}
	closeCh := p.closeCh
	p.mu.Unlock()
	<-closeCh
	p.closeOnce.Do(func() {
		p.mu.Lock()
		managers := make([]*StreamManager, 0, len(p.streamManagers))
		for _, mgr := range p.streamManagers {
			managers = append(managers, mgr)
		}
		p.streamManagers = make(map[string]*StreamManager)
		p.mu.Unlock()

		for _, mgr := range managers {
			_ = mgr.Close()
		}
		if p.manager != nil {
			p.closeErr = p.manager.Close()
		}
	})
	return p.closeErr
}

func (p *Provider) Registrations() []agent.ToolRegistration {
	if p == nil || len(p.regs) == 0 {
		return nil
	}
	out := make([]agent.ToolRegistration, len(p.regs))
	copy(out, p.regs)
	return out
}

func (p *Provider) Methods() []MethodSpec {
	if p == nil || len(p.methods) == 0 {
		return nil
	}
	out := make([]MethodSpec, len(p.methods))
	copy(out, p.methods)
	return out
}

func (p *Provider) discoverSource(ctx context.Context, source EndpointSource) ([]MethodSpec, error) {
	profile := source.Profile
	var (
		methods []MethodSpec
		err     error
	)
	if source.DescriptorSet != nil {
		if source.ToolNames == nil {
			return nil, fmt.Errorf("grpc profile %q embedded descriptor source requires an exact tool allowlist", profile.ID)
		}
		methods, err = DiscoverFromEmbeddedDescriptorSet(profile, source.DescriptorSet)
	} else {
		methods, err = p.discoverProfile(ctx, profile)
	}
	if err != nil {
		return nil, err
	}

	byMethod := make(map[string]MethodSpec, len(methods))
	for _, method := range methods {
		byMethod[method.FullMethod] = method
	}
	if source.ToolNames != nil {
		missing := make([]string, 0)
		filtered := make([]MethodSpec, 0, len(source.ToolNames))
		for fullMethod, toolName := range source.ToolNames {
			method, ok := byMethod[fullMethod]
			if !ok {
				missing = append(missing, fullMethod)
				continue
			}
			toolName = strings.TrimSpace(toolName)
			if toolName == "" || snakeIdentifier(toolName) != toolName {
				return nil, fmt.Errorf("grpc profile %q curated method %q has invalid tool name %q", profile.ID, fullMethod, toolName)
			}
			method.ToolBaseName = toolName
			method.OriginServerName = profile.ID
			if traits, ok := source.ToolTraits[fullMethod]; ok {
				method.Traits = traits
			}
			if description := strings.TrimSpace(source.ToolDescriptions[fullMethod]); description != "" {
				method.Description = description
			}
			filtered = append(filtered, method)
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return nil, fmt.Errorf("grpc profile %q curated methods missing from descriptor set: %s", profile.ID, strings.Join(missing, ", "))
		}
		sort.Slice(filtered, func(i, j int) bool { return filtered[i].FullMethod < filtered[j].FullMethod })
		methods = filtered
	} else {
		for i := range methods {
			methods[i].OriginServerName = profile.ID
		}
	}
	return methods, nil
}

func (p *Provider) discoverProfile(ctx context.Context, profile config.GRPCEndpointConfig) ([]MethodSpec, error) {
	if p == nil || p.manager == nil {
		return nil, errors.New("grpc provider is not configured")
	}
	var conn grpc.ClientConnInterface
	if profile.Discovery.EffectiveMode() != config.GRPCDiscoveryModeDescriptorSet {
		c, err := p.manager.Conn(ctx, profile.ID)
		if err != nil {
			// Reflection mode can still fall back to a static descriptor set.
			if strings.TrimSpace(profile.Discovery.DescriptorSet) == "" {
				return nil, fmt.Errorf("grpc profile %q: %w", profile.ID, err)
			}
		} else {
			conn = c
		}
	}
	methods, err := Discover(ctx, profile, conn)
	if err != nil {
		return nil, fmt.Errorf("grpc profile %q discovery: %w", profile.ID, err)
	}
	return methods, nil
}

func (p *Provider) assignGlobalToolNames() error {
	groups := make(map[string][]int)
	for i := range p.methods {
		base := strings.TrimSpace(p.methods[i].ToolBaseName)
		if base == "" {
			base = toolBaseName(config.GRPCEndpointConfig{ID: p.methods[i].ProfileID}, p.methods[i].ServiceName, p.methods[i].MethodName)
			p.methods[i].ToolBaseName = base
		}
		groups[base] = append(groups[base], i)
	}
	for base, indices := range groups {
		if len(indices) < 2 {
			continue
		}
		identities := make(map[string]struct{}, len(indices))
		for _, index := range indices {
			identity := p.methods[index].ProfileID + "\\x00" + p.methods[index].FullMethod
			if _, duplicate := identities[identity]; duplicate {
				return fmt.Errorf("duplicate grpc method identity %q", identity)
			}
			identities[identity] = struct{}{}
			p.methods[index].ToolBaseName = base + "_" + shortHash(identity)
		}
	}
	seen := make(map[string]string, len(p.methods))
	for _, method := range p.methods {
		identity := method.ProfileID + "\\x00" + method.FullMethod
		if previous, duplicate := seen[method.ToolBaseName]; duplicate {
			return fmt.Errorf("grpc tool name collision %q between %q and %q", method.ToolBaseName, previous, identity)
		}
		seen[method.ToolBaseName] = identity
	}
	return nil
}

func (p *Provider) buildRegistrations(profiles []config.GRPCEndpointConfig) []agent.ToolRegistration {
	if p == nil || p.manager == nil || len(p.methods) == 0 {
		return nil
	}
	profileByID := make(map[string]config.GRPCEndpointConfig, len(profiles))
	toolCounts := make(map[string]int, len(profiles))
	for _, profile := range profiles {
		profileByID[profile.ID] = profile
	}
	for _, method := range p.methods {
		toolCounts[method.ProfileID] += methodToolCount(method)
	}
	regs := make([]agent.ToolRegistration, 0, len(p.methods))
	for _, method := range p.methods {
		profile := profileByID[method.ProfileID]
		exposure := exposureForProfile(profile, toolCounts[method.ProfileID])
		if method.ClientStreaming || method.ServerStreaming {
			regs = append(regs, p.streamRegistrations(method, exposure)...)
			continue
		}
		regs = append(regs, p.unaryRegistration(method, exposure))
	}
	sort.Slice(regs, func(i, j int) bool { return regs[i].Descriptor.Name < regs[j].Descriptor.Name })
	return regs
}

func (p *Provider) unaryRegistration(method MethodSpec, exposure agent.ToolExposureMode) agent.ToolRegistration {
	exec := &UnaryExecutor{manager: p.manager}
	desc := unaryDescriptor(method)
	desc.Exposure = exposure
	return agent.ToolRegistration{
		Func: func(ctx context.Context, args map[string]any) (string, error) {
			if !p.beginCall() {
				return "", p.redactor.RedactError(errors.New("grpc provider is closing"))
			}
			defer p.endCall()
			result, err := exec.invoke(ctx, method, args)
			if err != nil {
				return "", p.redactor.RedactError(err)
			}
			return p.redactor.RedactString(result), nil
		},
		Descriptor:      desc,
		ProviderVisible: true,
		Validate: func(ctx context.Context, call agent.ToolCall, desc agent.ToolDescriptor) error {
			if err := exec.validate(ctx, method, call.Args); err != nil {
				return p.redactor.RedactError(err)
			}
			return nil
		},
	}
}

func (p *Provider) streamRegistrations(method MethodSpec, exposure agent.ToolExposureMode) []agent.ToolRegistration {
	base := strings.TrimSpace(method.ToolBaseName)
	if base == "" {
		base = snakeIdentifier(method.ProfileID + "_" + method.ServiceName + "_" + method.MethodName)
	}
	regs := []agent.ToolRegistration{
		p.streamRegistration(method, base+"_start", streamToolStart, startSchema(method), "Start a gRPC streaming session for "+method.FullMethod+".", exposure),
		p.streamRegistration(method, base+"_finish", streamToolFinish, finishSchema(method), "Finish and close a gRPC streaming session for "+method.FullMethod+".", exposure),
	}
	if method.ClientStreaming {
		regs = append(regs, p.streamRegistration(method, base+"_send", streamToolSend, sendSchema(method), "Send one message on a gRPC stream for "+method.FullMethod+".", exposure))
	}
	if method.ServerStreaming {
		regs = append(regs, p.streamRegistration(method, base+"_receive", streamToolReceive, receiveSchema(), "Receive messages from a gRPC stream for "+method.FullMethod+".", exposure))
	}
	return regs
}

func (p *Provider) streamRegistration(method MethodSpec, name, action string, schema map[string]any, description string, exposure agent.ToolExposureMode) agent.ToolRegistration {
	toolName := name
	return agent.ToolRegistration{
		Func: func(ctx context.Context, args map[string]any) (string, error) {
			if !p.beginCall() {
				return "", p.redactor.RedactError(errors.New("grpc provider is closing"))
			}
			defer p.endCall()
			manager := p.streamManagerForContext(ctx)
			var result string
			var err error
			switch action {
			case streamToolStart:
				result, err = manager.Start(ctx, method, args, toolName)
			case streamToolSend:
				result, err = manager.Send(ctx, args, toolName)
			case streamToolReceive:
				result, err = manager.Receive(ctx, args, toolName)
			case streamToolFinish:
				result, err = manager.Finish(ctx, args, toolName)
			default:
				err = fmt.Errorf("unknown stream tool action %q", action)
			}
			if err != nil {
				return "", p.redactor.RedactError(err)
			}
			return p.redactor.RedactString(result), nil
		},
		ProviderVisible: true,
		Descriptor: agent.ToolDescriptor{
			Name:            toolName,
			Description:     description,
			InputJSONSchema: schema,
			ParamAliases: map[string]string{
				"headers":    "metadata",
				"timeout_ms": "deadline_ms",
				"body":       "request",
				"input":      "request",
				"message":    "message",
			},
			Origin:   agent.ToolOrigin{Kind: agent.ToolOriginKindGRPC, ServerName: methodOriginServerName(method), CanonicalName: method.FullMethod},
			Traits:   streamMethodTraits(method),
			Exposure: exposure,
		},
	}
}

func (p *Provider) streamManagerForContext(ctx context.Context) *StreamManager {
	lifecycle, _ := agent.ToolLifecycleFromContext(ctx)
	key := lifecycle.SessionID + "\x00" + lifecycle.TurnID
	if strings.Trim(key, "\x00") == "" {
		key = "default"
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if manager := p.streamManagers[key]; manager != nil {
		return manager
	}
	manager := NewStreamManager(
		p.manager,
		WithStreamToolEventSink(lifecycle.Sink),
		WithStreamEventContext(lifecycle.SessionID, lifecycle.TurnID, lifecycle.Trace),
		WithStreamErrorRedactor(p.redactor.RedactString),
		WithStreamIdleCallback(func(mgr *StreamManager) { p.scheduleStreamManagerCleanup(key, mgr) }),
	)
	p.streamManagers[key] = manager
	return manager
}

func (p *Provider) scheduleStreamManagerCleanup(key string, manager *StreamManager) {
	if p == nil || manager == nil {
		return
	}
	ttl := p.streamManagerIdleTTL
	if ttl <= 0 {
		p.cleanupStreamManager(key, manager)
		return
	}
	time.AfterFunc(ttl, func() { p.cleanupStreamManager(key, manager) })
}

func (p *Provider) cleanupStreamManager(key string, manager *StreamManager) {
	if p == nil || manager == nil {
		return
	}
	p.mu.Lock()
	if p.streamManagers[key] != manager {
		p.mu.Unlock()
		return
	}
	if !manager.closeIfIdle() {
		p.mu.Unlock()
		return
	}
	delete(p.streamManagers, key)
	p.mu.Unlock()
}

func (p *Provider) signalClosed() {
	if p == nil {
		return
	}
	p.closeChOnce.Do(func() { close(p.closeCh) })
}

func (p *Provider) beginCall() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closing {
		return false
	}
	p.inFlight++
	return true
}

func (p *Provider) endCall() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.inFlight > 0 {
		p.inFlight--
	}
	if p.closing && p.inFlight == 0 {
		p.signalClosed()
	}
	p.mu.Unlock()
}

func methodOriginServerName(method MethodSpec) string {
	if name := strings.TrimSpace(method.OriginServerName); name != "" {
		return name
	}
	return method.ProfileID
}

func streamMethodTraits(method MethodSpec) agent.ToolTraits {
	traits := method.Traits
	traits.ConcurrencySafe = false
	traits.InterruptBehavior = agent.ToolInterruptBehaviorCancel
	return traits
}

func methodToolCount(method MethodSpec) int {
	if !method.ClientStreaming && !method.ServerStreaming {
		return 1
	}
	count := 2 // start + finish
	if method.ClientStreaming {
		count++
	}
	if method.ServerStreaming {
		count++
	}
	return count
}

func exposureForProfile(profile config.GRPCEndpointConfig, toolCount int) agent.ToolExposureMode {
	switch profile.Exposure.EffectiveMode() {
	case config.GRPCExposureModeInline:
		return agent.ToolExposureModeInline
	case config.GRPCExposureModeDeferred:
		return agent.ToolExposureModeDeferred
	default:
		threshold := profile.Exposure.EffectiveDeferredThreshold()
		if threshold > 0 && toolCount > threshold {
			return agent.ToolExposureModeDeferred
		}
		return agent.ToolExposureModeInline
	}
}
