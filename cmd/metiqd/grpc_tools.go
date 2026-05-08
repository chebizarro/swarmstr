package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"sort"
	"strings"
	"sync"

	"metiq/internal/agent"
	"metiq/internal/agent/toolgrpc"
	"metiq/internal/config"
	"metiq/internal/store/state"
)

var newGRPCProvider = toolgrpc.NewProvider

func grpcConfigFromConfigDoc(doc state.ConfigDoc) (config.GRPCConfig, error) {
	if len(doc.Extra) == 0 || doc.Extra["grpc"] == nil {
		return config.GRPCConfig{}, nil
	}
	raw, err := json.Marshal(doc.Extra["grpc"])
	if err != nil {
		return config.GRPCConfig{}, fmt.Errorf("marshal grpc config: %w", err)
	}
	cfg, err := config.ParseGRPCConfigBytes(raw, ".json")
	if err != nil {
		return config.GRPCConfig{}, err
	}
	if errs := cfg.Validate(); len(errs) > 0 {
		return config.GRPCConfig{}, fmt.Errorf("validate grpc config: %w", errors.Join(errs...))
	}
	return cfg, nil
}

type grpcToolRegistryReconcileResult struct {
	Added     int
	Updated   int
	Removed   int
	Unchanged int
	Desired   int
	Conflicts int
}

func (r grpcToolRegistryReconcileResult) Changed() bool {
	return r.Added+r.Updated+r.Removed > 0
}

func descriptorsEqual(a, b agent.ToolDescriptor) bool {
	return a.Name == b.Name &&
		a.Description == b.Description &&
		a.Origin == b.Origin &&
		reflect.DeepEqual(a.InputJSONSchema, b.InputJSONSchema)
}

func reconcileGRPCToolRegistry(reg *agent.ToolRegistry, provider *toolgrpc.Provider) grpcToolRegistryReconcileResult {
	desired := map[string]agent.ToolRegistration{}
	if provider != nil {
		for _, registration := range provider.Registrations() {
			name := strings.TrimSpace(registration.Descriptor.Name)
			if name == "" {
				continue
			}
			desired[name] = registration
		}
	}
	return reconcileGRPCToolRegistryDesired(reg, desired)
}

func reconcileGRPCToolRegistryDesired(reg *agent.ToolRegistry, desired map[string]agent.ToolRegistration) grpcToolRegistryReconcileResult {
	result := grpcToolRegistryReconcileResult{}
	if reg == nil {
		return result
	}

	result.Desired = len(desired)

	existingGRPC := map[string]agent.ToolDescriptor{}
	for _, desc := range reg.Descriptors() {
		if desc.Origin.Kind == agent.ToolOriginKindGRPC {
			existingGRPC[desc.Name] = desc
		}
	}

	existingNames := make([]string, 0, len(existingGRPC))
	for name := range existingGRPC {
		existingNames = append(existingNames, name)
	}
	sort.Strings(existingNames)
	for _, name := range existingNames {
		if _, ok := desired[name]; ok {
			continue
		}
		if reg.Remove(name) {
			result.Removed++
			log.Printf("[grpc] unregistered tool: %s", name)
		}
	}

	desiredNames := make([]string, 0, len(desired))
	for name := range desired {
		desiredNames = append(desiredNames, name)
	}
	sort.Strings(desiredNames)
	for _, name := range desiredNames {
		registration := desired[name]
		if current, ok := reg.Descriptor(name); ok && current.Origin.Kind != agent.ToolOriginKindGRPC {
			result.Conflicts++
			log.Printf("[grpc] skipped tool reconcile for %s: already owned by %s", name, current.Origin.Kind)
			continue
		}
		existing, ok := existingGRPC[name]
		switch {
		case !ok:
			reg.RegisterTool(name, registration)
			result.Added++
			log.Printf("[grpc] registered tool: %s", name)
		case descriptorsEqual(existing, registration.Descriptor):
			result.Unchanged++
		default:
			reg.RegisterTool(name, registration)
			result.Updated++
			log.Printf("[grpc] updated tool: %s", name)
		}
	}

	return result
}

type grpcProviderController struct {
	mu       sync.Mutex
	provider *toolgrpc.Provider
}

func (c *grpcProviderController) reconcile(ctx context.Context, reg *agent.ToolRegistry, doc state.ConfigDoc, logContext string) grpcToolRegistryReconcileResult {
	if c == nil {
		return grpcToolRegistryReconcileResult{}
	}
	cfg, cfgErr := grpcConfigFromConfigDoc(doc)
	if cfgErr != nil {
		log.Printf("[grpc] %s config invalid; keeping previous provider: %v", logContext, cfgErr)
		return grpcToolRegistryReconcileResult{}
	}
	var next *toolgrpc.Provider
	if len(cfg.Endpoints) > 0 {
		provider, err := newGRPCProvider(ctx, cfg)
		if err != nil {
			log.Printf("[grpc] %s provider build failed; keeping previous provider: %v", logContext, err)
			return grpcToolRegistryReconcileResult{}
		}
		next = provider
	}

	c.mu.Lock()
	prev := c.provider
	c.provider = next
	c.mu.Unlock()

	result := reconcileGRPCToolRegistry(reg, next)
	if result.Changed() || result.Conflicts > 0 {
		log.Printf("[grpc] %s tool reconcile: added=%d updated=%d removed=%d unchanged=%d desired=%d conflicts=%d", logContext, result.Added, result.Updated, result.Removed, result.Unchanged, result.Desired, result.Conflicts)
	}
	if result.Conflicts > 0 {
		log.Printf("[grpc] %s warning: %d desired gRPC tools were not registered due to ownership conflicts", logContext, result.Conflicts)
	}
	if prev != nil {
		if err := prev.Close(); err != nil {
			log.Printf("[grpc] %s close previous provider warning: %v", logContext, err)
		}
	}
	return result
}

func (c *grpcProviderController) close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	provider := c.provider
	c.provider = nil
	c.mu.Unlock()
	if provider != nil {
		_ = provider.Close()
	}
}
