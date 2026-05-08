package config

import (
	"strings"
	"testing"

	"metiq/internal/store/state"
)

func TestParseGRPCConfigBytesYAMLValid(t *testing.T) {
	raw := []byte(`
endpoints:
  - id: billing
    target: billing.internal:443
    discovery:
      mode: reflection
      refresh_ttl: 10m
    transport:
      tls_mode: system
      server_name: billing.internal
    auth:
      metadata:
        authorization: secret:billing_token
      allow_override_keys: [x-request-id]
    defaults:
      dial_timeout_ms: 10000
      reflection_timeout_ms: 5000
      deadline_ms: 15000
      max_deadline_ms: 120000
      max_recv_message_bytes: 4194304
    exposure:
      mode: auto
      deferred_threshold: 25
      namespace: grpc_billing
      include_services: [acme.billing.InvoiceService]
      exclude_methods: [/acme.billing.Admin/DeleteEverything]
`)
	cfg, err := ParseGRPCConfigBytes(raw, "config.yaml")
	if err != nil {
		t.Fatalf("ParseGRPCConfigBytes: %v", err)
	}
	if len(cfg.Endpoints) != 1 || cfg.Endpoints[0].ID != "billing" {
		t.Fatalf("unexpected endpoints: %#v", cfg.Endpoints)
	}
	if got := cfg.Endpoints[0].Discovery.EffectiveMode(); got != GRPCDiscoveryModeReflection {
		t.Fatalf("unexpected discovery mode: %q", got)
	}
	if errs := cfg.Validate(); len(errs) != 0 {
		t.Fatalf("expected valid config, got: %v", errs)
	}
}

func TestParseGRPCConfigBytesYAMLWrapper(t *testing.T) {
	raw := []byte(`
grpc:
  endpoints:
    - id: reports
      target: reports.internal:443
`)
	cfg, err := ParseGRPCConfigBytes(raw, ".yaml")
	if err != nil {
		t.Fatalf("ParseGRPCConfigBytes: %v", err)
	}
	if len(cfg.Endpoints) != 1 || cfg.Endpoints[0].ID != "reports" {
		t.Fatalf("expected wrapped grpc endpoint, got %#v", cfg.Endpoints)
	}
	if errs := cfg.Validate(); len(errs) != 0 {
		t.Fatalf("expected valid wrapped config, got: %v", errs)
	}
}

func TestValidateGRPCConfigDiscoveryModes(t *testing.T) {
	cfg := GRPCConfig{Endpoints: []GRPCEndpointConfig{
		{ID: "bad-mode", Target: "svc:443", Discovery: GRPCDiscoveryConfig{Mode: "unknown"}},
		{ID: "missing-desc", Target: "svc:443", Discovery: GRPCDiscoveryConfig{Mode: GRPCDiscoveryModeDescriptorSet}},
		{ID: "missing-protos", Target: "svc:443", Discovery: GRPCDiscoveryConfig{Mode: GRPCDiscoveryModeProtoFiles}},
	}}
	errs := cfg.Validate()
	for _, want := range []string{"unknown value", "descriptor_set", "proto_files"} {
		if !containsError(errs, want) {
			t.Fatalf("expected gRPC discovery error containing %q, got: %v", want, errs)
		}
	}
}

func TestValidateGRPCConfigTransportAuthExposure(t *testing.T) {
	cfg := GRPCConfig{Endpoints: []GRPCEndpointConfig{{
		ID:     "bad",
		Target: "svc:443",
		Transport: GRPCTransportConfig{
			TLSMode:  GRPCTransportTLSModeInsecure,
			CAFile:   "/tmp/ca.pem",
			CertFile: "/tmp/client.pem",
		},
		Auth: GRPCAuthConfig{
			Metadata:          map[string]string{"Authorization": "secret:token"},
			AllowOverrideKeys: []string{"x-request-id", "x-request-id"},
		},
		Defaults: GRPCDefaultsConfig{
			DeadlineMS:    10_000,
			MaxDeadlineMS: 5_000,
		},
		Exposure: GRPCExposureConfig{
			Mode:              "eager",
			DeferredThreshold: -1,
			Namespace:         "grpc-bad",
		},
	}}}
	errs := cfg.Validate()
	for _, want := range []string{
		"tls_mode insecure cannot be combined",
		"cert_file and key_file",
		"metadata key must be lowercase",
		"duplicates another override key",
		"deadline_ms must be <= max_deadline_ms",
		"exposure.mode",
		"deferred_threshold",
		"namespace",
	} {
		if !containsError(errs, want) {
			t.Fatalf("expected validation error containing %q, got: %v", want, errs)
		}
	}
}

func TestParseConfigBytesYAMLAllowsTopLevelGRPC(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`
grpc:
  endpoints:
    - id: billing
      target: billing.internal:443
      discovery:
        mode: reflection
`), ".yaml")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if doc.Extra == nil || doc.Extra["grpc"] == nil {
		t.Fatalf("expected grpc section preserved in Extra, got %#v", doc.Extra)
	}
	if errs := ValidateConfigDoc(doc); len(errs) != 0 {
		t.Fatalf("expected top-level grpc config to validate, got: %v", errs)
	}
}

func TestValidateConfigDocGRPCFromExtraInvalid(t *testing.T) {
	doc := state.ConfigDoc{Extra: map[string]any{
		"grpc": map[string]any{
			"endpoints": []any{
				map[string]any{"id": "bad", "target": "svc:443", "discovery": map[string]any{"mode": "descriptor_set"}},
			},
		},
	}}
	errs := ValidateConfigDoc(doc)
	if !containsError(errs, "descriptor_set") {
		t.Fatalf("expected grpc descriptor_set validation error, got: %v", errs)
	}
}

func TestParseConfigBytesGRPCRejectsUnknownFields(t *testing.T) {
	_, err := ParseConfigBytes([]byte(`
grpc:
  endpoints:
    - id: billing
      target: billing.internal:443
      discovery:
        mode: reflection
        poll_interval: 1s
`), ".yaml")
	if err == nil {
		t.Fatal("expected unsupported grpc field error")
	}
	if !strings.Contains(err.Error(), "grpc.endpoints[0].discovery.poll_interval") {
		t.Fatalf("expected unknown grpc field in error, got: %v", err)
	}
}

func containsError(errs []error, want string) bool {
	for _, err := range errs {
		if strings.Contains(err.Error(), want) {
			return true
		}
	}
	return false
}
