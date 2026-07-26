package methods

import (
	"encoding/json"
	"testing"

	"metiq/internal/gateway/protocol"
)

// TestOpenclawAliasDecodeAndTranslate exercises decode + Normalize + ToNative
// for each OpenClaw compat alias, confirming the alias request maps onto the
// native request shape it re-dispatches to.
func TestOpenclawAliasDecodeAndTranslate(t *testing.T) {
	t.Run("chat", func(t *testing.T) {
		r, err := DecodeOpenclawChatParams(json.RawMessage(`{"to":"  peer-1 ","text":"hi"}`))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if r, err = r.Normalize(); err != nil {
			t.Fatalf("normalize: %v", err)
		}
		native := r.ToNative()
		if native.To != "peer-1" || native.Text != "hi" {
			t.Fatalf("unexpected native chat.send request: %+v", native)
		}
	})

	t.Run("chat.history", func(t *testing.T) {
		r, err := DecodeOpenclawChatHistoryParams(json.RawMessage(`{"session_id":" sess-1 ","limit":5}`))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if r, err = r.Normalize(); err != nil {
			t.Fatalf("normalize: %v", err)
		}
		native := r.ToNative()
		if native.SessionID != "sess-1" || native.Limit != 5 {
			t.Fatalf("unexpected native chat.history request: %+v", native)
		}
	})

	t.Run("changes.list", func(t *testing.T) {
		r, err := DecodeOpenclawChangesListParams(json.RawMessage(`{"sessionKey":" sess-1 ","path":"pkg","search":"foo"}`))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if r, err = r.Normalize(); err != nil {
			t.Fatalf("normalize: %v", err)
		}
		native := r.ToNative()
		if native.SessionKey != "sess-1" || native.Path != "pkg" || native.Search != "foo" {
			t.Fatalf("unexpected native sessions.files.list request: %+v", native)
		}
	})

	t.Run("approval.list", func(t *testing.T) {
		r, err := DecodeOpenclawApprovalListParams(json.RawMessage(`{"kind":"exec","status":"pending"}`))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if r, err = r.Normalize(); err != nil {
			t.Fatalf("normalize: %v", err)
		}
		native := r.ToNative()
		if native.Kind != "exec" || native.Status != "pending" {
			t.Fatalf("unexpected native approval.list request: %+v", native)
		}
	})

	t.Run("empty params decode to zero value", func(t *testing.T) {
		if _, err := DecodeOpenclawApprovalListParams(nil); err != nil {
			t.Fatalf("empty approval.list params should decode: %v", err)
		}
		if _, err := DecodeOpenclawChatHistoryParams(json.RawMessage(``)); err != nil {
			t.Fatalf("empty chat.history params should decode: %v", err)
		}
	})
}

// TestOpenclawAliasesRegisteredAndScoped confirms the four compat aliases are
// dispatchable (in SupportedMethods, so parity can mark them implemented) and
// carry the same scope as the native method they re-dispatch to, while the five
// openclaw.setup.* onboarding methods remain unregistered (honest UNAVAILABLE).
func TestOpenclawAliasesRegisteredAndScoped(t *testing.T) {
	supported := map[string]struct{}{}
	for _, m := range SupportedMethods() {
		supported[m] = struct{}{}
	}

	wantScope := map[string]string{
		MethodOpenclawChat:         protocol.MethodScopeOperatorWrite,
		MethodOpenclawChatHistory:  protocol.MethodScopeOperatorRead,
		MethodOpenclawChangesList:  protocol.MethodScopeOperatorRead,
		MethodOpenclawApprovalList: protocol.MethodScopeOperatorApprovals,
	}
	for method, scope := range wantScope {
		if _, ok := supported[method]; !ok {
			t.Fatalf("alias %s must be in SupportedMethods", method)
		}
		if got := MethodDescriptor(method).Scope; got != scope {
			t.Fatalf("alias %s scope = %q, want %q", method, got, scope)
		}
		if !InAdminDispatchGroup(AdminDispatchOpenclaw, method) {
			t.Fatalf("alias %s must be in the openclaw dispatch group", method)
		}
	}

	// The setup.* onboarding methods are a genuine deviation: unregistered.
	for _, method := range []string{
		"openclaw.setup.detect",
		"openclaw.setup.activate",
		"openclaw.setup.auth.start",
		"openclaw.setup.prepare.start",
		"openclaw.setup.verify",
	} {
		if _, ok := supported[method]; ok {
			t.Fatalf("openclaw.setup method %s must NOT be supported (honest UNAVAILABLE)", method)
		}
	}
}
