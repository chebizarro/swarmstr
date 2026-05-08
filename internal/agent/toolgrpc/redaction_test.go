package toolgrpc

import (
	"strings"
	"testing"

	"metiq/internal/agent"
)

func TestRedactorRedactsSensitiveMetadataAndTLSMaterial(t *testing.T) {
	redactor := NewRedactor()
	metadata := redactor.RedactMetadata(map[string]string{
		"authorization": "Bearer super-secret",
		"x-request-id":  "req-123",
		"trace-bin":     "binary-secret",
	})
	if metadata["authorization"] != redactedValue {
		t.Fatalf("authorization not redacted: %#v", metadata)
	}
	if metadata["trace-bin"] != redactedValue {
		t.Fatalf("binary metadata not redacted: %#v", metadata)
	}
	if metadata["x-request-id"] != "req-123" {
		t.Fatalf("safe metadata changed: %#v", metadata)
	}

	result := redactor.RedactString(`{"metadata":{"authorization":"Bearer super-secret","x-request-id":"req-123"},"transport":{"cert_file":"/tmp/client.pem","key_file":"/tmp/client.key"},"response":{"access_token":"secret-token","id":"ok"}}`)
	if strings.Contains(result, "super-secret") || strings.Contains(result, "client.pem") || strings.Contains(result, "secret-token") {
		t.Fatalf("redacted result still contains secret material: %s", result)
	}
	if !strings.Contains(result, "req-123") || !strings.Contains(result, "ok") {
		t.Fatalf("redaction removed safe values: %s", result)
	}
}

func TestStreamManagerRedactsLifecycleErrors(t *testing.T) {
	var captured string
	manager := NewStreamManager(nil,
		WithStreamToolEventSink(func(evt agent.ToolLifecycleEvent) { captured = evt.Error }),
		WithStreamErrorRedactor(NewRedactor().RedactString),
	)
	manager.emit("grpc_test", streamToolReceive, "", &StreamSession{ID: "s1", Method: MethodSpec{ProfileID: "billing", FullMethod: "/svc/Secret"}}, 0, true, "rpc failed: Bearer super-secret")
	if captured == "" {
		t.Fatal("expected lifecycle error event")
	}
	if strings.Contains(captured, "super-secret") {
		t.Fatalf("lifecycle error leaked secret: %s", captured)
	}
}
