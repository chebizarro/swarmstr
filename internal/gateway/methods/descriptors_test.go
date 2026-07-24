package methods

import (
	"testing"

	"metiq/internal/gateway/protocol"
)

func TestMethodDescriptorsCoverSupportedMethods(t *testing.T) {
	methods := SupportedMethods()
	descriptors := MethodDescriptors(methods)
	if len(descriptors) != len(methods) {
		t.Fatalf("descriptor count=%d, methods=%d", len(descriptors), len(methods))
	}
	seen := map[string]bool{}
	for _, descriptor := range descriptors {
		if descriptor.Name == "" || descriptor.Scope == "" {
			t.Fatalf("incomplete descriptor: %+v", descriptor)
		}
		if seen[descriptor.Name] {
			t.Fatalf("duplicate descriptor for %q", descriptor.Name)
		}
		seen[descriptor.Name] = true
	}
	for _, method := range methods {
		if !seen[method] {
			t.Fatalf("method %q is missing descriptor metadata", method)
		}
	}
}

func TestMethodDescriptorPolicyClassification(t *testing.T) {
	tests := []struct {
		method       string
		scope        string
		startup      string
		controlWrite bool
	}{
		{MethodHealth, protocol.MethodScopeOperatorRead, "", false},
		{MethodChatSend, protocol.MethodScopeOperatorWrite, protocol.MethodStartupUnavailableUntilSidecars, false},
		{MethodExecApprovalResolve, protocol.MethodScopeOperatorApprovals, "", false},
		{MethodDevicePairRename, protocol.MethodScopeOperatorPairing, "", false},
		{MethodNodeInvokeProgress, protocol.MethodScopeNode, "", false},
		{MethodSessionsCreate, protocol.MethodScopeOperatorAdmin, protocol.MethodStartupUnavailableUntilSidecars, false},
		{MethodSessionsFilesList, protocol.MethodScopeOperatorRead, protocol.MethodStartupUnavailableUntilSidecars, false},
		{MethodSessionsFilesSet, protocol.MethodScopeOperatorAdmin, protocol.MethodStartupUnavailableUntilSidecars, false},
		{MethodSessionsCatalogContinue, protocol.MethodScopeOperatorWrite, protocol.MethodStartupUnavailableUntilSidecars, false},
		{MethodConfigApply, protocol.MethodScopeOperatorAdmin, "", true},
		{"extension.custom", protocol.MethodScopeOperatorAdmin, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			descriptor := MethodDescriptor(tt.method)
			if descriptor.Scope != tt.scope || descriptor.Startup != tt.startup || descriptor.ControlPlaneWrite != tt.controlWrite {
				t.Fatalf("descriptor=%+v", descriptor)
			}
		})
	}
}
