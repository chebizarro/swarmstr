package ws

import (
	"context"
	"net/http/httptest"
	"testing"

	"metiq/internal/gateway/methods"
	"metiq/internal/gateway/protocol"
)

func descriptorRuntime(descriptors ...protocol.MethodDescriptor) *Runtime {
	byName := make(map[string]protocol.MethodDescriptor, len(descriptors))
	for _, descriptor := range descriptors {
		byName[descriptor.Name] = descriptor
	}
	return &Runtime{opts: RuntimeOptions{ControlWriteLimitPerMin: 1}, methodDescriptors: byName}
}

func TestAdmitMethodEnforcesOperatorScopesAndNodeRole(t *testing.T) {
	r := descriptorRuntime(
		protocol.MethodDescriptor{Name: "read", Scope: protocol.MethodScopeOperatorRead},
		protocol.MethodDescriptor{Name: "write", Scope: protocol.MethodScopeOperatorWrite},
		protocol.MethodDescriptor{Name: "node", Scope: protocol.MethodScopeNode},
	)
	c := &client{}
	reader := ControlPrincipal{Authenticated: true, Role: "operator", Scopes: []string{protocol.MethodScopeOperatorRead}, ScopesEnforced: true}
	if shape := r.admitMethod(c, reader, "read"); shape != nil {
		t.Fatalf("read scope rejected: %+v", shape)
	}
	if shape := r.admitMethod(c, reader, "write"); shape == nil || shape.Message != "missing scope: operator.write" {
		t.Fatalf("write without scope should be rejected, got %+v", shape)
	}
	if shape := r.admitMethod(c, reader, "node"); shape == nil {
		t.Fatal("operator must not call node-scoped method")
	}
	unauthenticated := ControlPrincipal{Role: "operator"}
	if shape := r.admitMethod(c, unauthenticated, "write"); shape == nil || shape.Message != "authentication required" {
		t.Fatalf("unauthenticated write should be rejected, got %+v", shape)
	}
	if shape := r.admitMethod(c, unauthenticated, "read"); shape != nil {
		t.Fatalf("read should continue to downstream unauth policy, got %+v", shape)
	}
	node := ControlPrincipal{Authenticated: true, Role: "node", ScopesEnforced: true}
	if shape := r.admitMethod(c, node, "node"); shape != nil {
		t.Fatalf("authenticated node rejected: %+v", shape)
	}
	if shape := r.admitMethod(c, node, "read"); shape == nil {
		t.Fatal("node must not call operator-scoped method")
	}
}

func TestAdmitMethodEnforcesStartupAndControlWriteFlood(t *testing.T) {
	ready := false
	r := descriptorRuntime(
		protocol.MethodDescriptor{Name: "startup", Scope: protocol.MethodScopeOperatorRead, Startup: protocol.MethodStartupUnavailableUntilSidecars},
		protocol.MethodDescriptor{Name: "control", Scope: protocol.MethodScopeOperatorAdmin, ControlPlaneWrite: true},
	)
	r.opts.StartupReady = func() bool { return ready }
	principal := ControlPrincipal{Authenticated: true, Role: "operator", Scopes: defaultOperatorScopes(), ScopesEnforced: true}
	c := &client{}
	if shape := r.admitMethod(c, principal, "startup"); shape == nil || !shape.Retryable {
		t.Fatalf("startup method should be retryable unavailable, got %+v", shape)
	}
	ready = true
	if shape := r.admitMethod(c, principal, "startup"); shape != nil {
		t.Fatalf("ready startup method rejected: %+v", shape)
	}
	if shape := r.admitMethod(c, principal, "control"); shape != nil {
		t.Fatalf("first control write rejected: %+v", shape)
	}
	if shape := r.admitMethod(c, principal, "control"); shape == nil || !shape.Retryable {
		t.Fatalf("control write flood should be retryable unavailable, got %+v", shape)
	}
}

func TestEvaluateAuthUsesValidatedPairedDeviceToken(t *testing.T) {
	r := &Runtime{opts: RuntimeOptions{
		Token: "gateway-secret",
		ValidateDeviceToken: func(connect protocol.ConnectParams, token string) DeviceTokenDecision {
			if token != "paired-secret" {
				return DeviceTokenDecision{Reason: "device_token_mismatch", Code: "DEVICE_AUTH_TOKEN_MISMATCH"}
			}
			return DeviceTokenDecision{OK: true, Role: "operator", Scopes: []string{protocol.MethodScopeOperatorRead}, Subject: connect.Device.ID}
		},
	}}
	req := httptest.NewRequest("GET", "http://example/ws", nil)
	connect := protocol.ConnectParams{
		Role:   "operator",
		Scopes: []string{protocol.MethodScopeOperatorRead},
		Device: &protocol.ConnectDevice{ID: "device", PublicKey: "public", Signature: "signature"},
		Auth:   &protocol.ConnectAuth{DeviceToken: "paired-secret"},
	}
	decision := r.evaluateAuth(req, connect)
	if !decision.OK || decision.Method != "device-token" || !decision.ScopesEnforced || len(decision.Scopes) != 1 {
		t.Fatalf("unexpected paired-device decision: %+v", decision)
	}
	connect.Auth.DeviceToken = "revoked"
	decision = r.evaluateAuth(req, connect)
	if decision.OK || decision.Code != "DEVICE_AUTH_TOKEN_MISMATCH" {
		t.Fatalf("revoked token accepted: %+v", decision)
	}
}

func TestInternalMethodDescriptorIsDispatchableButNotAdvertised(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	publicNames := append([]string{}, methods.SupportedMethods()...)
	publicNames = append(publicNames, MethodEventsList, MethodEventsSubscribe, MethodEventsUnsubscribe)
	internal := methods.MethodDescriptor(methods.MethodNodeRunnerInventoryUpdate)
	r, err := Start(ctx, RuntimeOptions{
		Addr:                      "127.0.0.1:0",
		Methods:                   publicNames,
		MethodDescriptors:         methods.MethodDescriptors(publicNames),
		InternalMethodDescriptors: []protocol.MethodDescriptor{internal},
	})
	if err != nil {
		t.Fatalf("start runtime: %v", err)
	}
	if !r.isMethodAllowed(internal.Name) {
		t.Fatalf("internal method %q is not dispatchable", internal.Name)
	}
	for _, name := range r.opts.Methods {
		if name == internal.Name {
			t.Fatalf("internal method %q leaked into advertised methods", internal.Name)
		}
	}
	for _, descriptor := range r.listMethodDescriptors() {
		if descriptor.Name == internal.Name {
			t.Fatalf("internal method %q leaked into advertised descriptors", internal.Name)
		}
	}
	node := ControlPrincipal{Authenticated: true, Role: "node", ScopesEnforced: true}
	if shape := r.admitMethod(&client{}, node, internal.Name); shape != nil {
		t.Fatalf("authenticated node rejected from internal method: %+v", shape)
	}
	operator := ControlPrincipal{Authenticated: true, Role: "operator", Scopes: defaultOperatorScopes(), ScopesEnforced: true}
	if shape := r.admitMethod(&client{}, operator, internal.Name); shape == nil {
		t.Fatal("operator must not call internal node-scoped method")
	}
}

func TestCoreMethodDescriptorsSatisfyRuntimeRegistry(t *testing.T) {
	names := append([]string{}, methods.SupportedMethods()...)
	names = append(names, MethodEventsList, MethodEventsSubscribe, MethodEventsUnsubscribe)
	if _, err := buildMethodDescriptors(methods.MethodDescriptors(names), names); err != nil {
		t.Fatalf("production descriptor catalog rejected: %v", err)
	}
}

func TestBuildMethodDescriptorsRequiresCompleteValidMetadata(t *testing.T) {
	allNames := buildAllowedMethods([]string{"status"})
	descriptors := make([]protocol.MethodDescriptor, 0, len(allNames))
	for name := range allNames {
		descriptors = append(descriptors, protocol.MethodDescriptor{Name: name, Scope: protocol.MethodScopeOperatorRead})
	}
	if _, err := buildMethodDescriptors(descriptors, []string{"status"}); err != nil {
		t.Fatalf("complete descriptors rejected: %v", err)
	}
	if _, err := buildMethodDescriptors(descriptors[:len(descriptors)-1], []string{"status"}); err == nil {
		t.Fatal("missing descriptor should fail")
	}
	descriptors[0].Scope = "invalid"
	if _, err := buildMethodDescriptors(descriptors, []string{"status"}); err == nil {
		t.Fatal("invalid descriptor scope should fail")
	}
}
