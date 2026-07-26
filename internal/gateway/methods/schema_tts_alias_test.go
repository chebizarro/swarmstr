package methods

import (
	"testing"

	"metiq/internal/gateway/protocol"
)

// TestTTSSpeakAliasRegisteredAndScoped confirms tts.speak is a dispatchable
// compat alias (openclaw naming) for talk.speak: it must be in SupportedMethods
// (so parity can mark it implemented), share talk.speak's operator.write scope,
// and live in the same talk dispatch group so it routes through handleTalkRPC.
func TestTTSSpeakAliasRegisteredAndScoped(t *testing.T) {
	supported := map[string]struct{}{}
	for _, m := range SupportedMethods() {
		supported[m] = struct{}{}
	}

	if _, ok := supported[MethodTTSSpeak]; !ok {
		t.Fatalf("alias %s must be in SupportedMethods", MethodTTSSpeak)
	}
	if got, want := MethodDescriptor(MethodTTSSpeak).Scope, MethodDescriptor(MethodTalkSpeak).Scope; got != want {
		t.Fatalf("alias %s scope = %q, want talk.speak scope %q", MethodTTSSpeak, got, want)
	}
	if got := MethodDescriptor(MethodTTSSpeak).Scope; got != protocol.MethodScopeOperatorWrite {
		t.Fatalf("alias %s scope = %q, want operator.write", MethodTTSSpeak, got)
	}
	if !InAdminDispatchGroup(AdminDispatchTalk, MethodTTSSpeak) {
		t.Fatalf("alias %s must be in the talk dispatch group (routes through handleTalkRPC)", MethodTTSSpeak)
	}
}
