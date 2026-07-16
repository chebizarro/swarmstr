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

func TestRedactorRedactsLightningCredentialsAndPreimages(t *testing.T) {
	redactor := NewRedactor()
	preimage := strings.Repeat("ab", 32)
	macaroon := "AgEDbG5kAv4BAwoQvery-secret-macaroon"
	result := redactor.RedactString(`{"macaroon":"` + macaroon + `","payment_preimage":"` + preimage + `","nested":{"preimage":"` + preimage + `","l402":"` + macaroon + `:` + preimage + `","lsat":"token"},"payment_hash":"safe-hash"}`)
	for _, secret := range []string{macaroon, preimage, "token"} {
		if strings.Contains(result, secret) {
			t.Fatalf("structured Lightning redaction leaked %q: %s", secret, result)
		}
	}
	if !strings.Contains(result, "safe-hash") {
		t.Fatalf("redaction removed safe payment hash: %s", result)
	}

	raw := "rpc failed authorization=L402 " + macaroon + ":" + preimage
	redacted := redactor.RedactString(raw)
	if strings.Contains(redacted, macaroon) || strings.Contains(redacted, preimage) {
		t.Fatalf("unstructured L402 token leaked: %s", redacted)
	}
	if got := redactor.RedactMetadata(map[string]string{"macaroon": macaroon, "x-request-id": "safe"}); got["macaroon"] != redactedValue || got["x-request-id"] != "safe" {
		t.Fatalf("metadata redaction = %#v", got)
	}
}

func TestRedactorRedactsUnstructuredTokenAssignments(t *testing.T) {
	redactor := NewRedactor()
	raw := "wallet failed token=secret-token access_token=secret-access refresh-token=secret-refresh api-key=secret-api password=secret-password"
	redacted := redactor.RedactString(raw)
	for _, secret := range []string{"secret-token", "secret-access", "secret-refresh", "secret-api", "secret-password"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("unstructured token assignment leaked %q: %s", secret, redacted)
		}
	}
}

func TestRedactorRedactsQuotedL402AndLSATChallenges(t *testing.T) {
	redactor := NewRedactor()
	for _, scheme := range []string{"L402", "LSAT"} {
		challenge := `rpc failed: ` + scheme + ` macaroon="BASE64_SUPER_SECRET", invoice="lnbc1invoice"`
		redacted := redactor.RedactString(challenge)
		if strings.Contains(redacted, "BASE64_SUPER_SECRET") {
			t.Fatalf("quoted %s challenge leaked macaroon: %s", scheme, redacted)
		}
		unquoted := `rpc failed: ` + scheme + ` macaroon=BASE64_SUPER_SECRET, invoice=lnbc1invoice`
		redacted = redactor.RedactString(unquoted)
		if strings.Contains(redacted, "BASE64_SUPER_SECRET") {
			t.Fatalf("unquoted %s challenge leaked macaroon: %s", scheme, redacted)
		}
	}
}
