package methods

import "testing"

func TestControlMethodReplayPolicy(t *testing.T) {
	if got := ControlMethodReplayPolicy(MethodSecretsResolve); got != ControlReplayNone {
		t.Fatalf("secrets.resolve replay policy = %v, want none", got)
	}
	if got := ControlMethodReplayPolicy(MethodConfigGet); got != ControlReplayEventAndRequest {
		t.Fatalf("config.get replay policy = %v, want event+request", got)
	}
	if got := ControlMethodReplayPolicy(MethodConfigSet); got != ControlReplayEventOnly {
		t.Fatalf("config.set replay policy = %v, want event-only", got)
	}
	if got := ControlMethodReplayPolicy("  status.get  "); got != ControlReplayEventAndRequest {
		t.Fatalf("trimmed status.get replay policy = %v, want event+request", got)
	}
	if got := ControlMethodReplayPolicy("unknown.method"); got != ControlReplayEventOnly {
		t.Fatalf("unknown method replay policy = %v, want event-only", got)
	}
}
