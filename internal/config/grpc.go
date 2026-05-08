package config

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/tailscale/hujson"
	"gopkg.in/yaml.v3"
)

const (
	GRPCDiscoveryModeReflection    = "reflection"
	GRPCDiscoveryModeDescriptorSet = "descriptor_set"
	GRPCDiscoveryModeProtoFiles    = "proto_files"

	GRPCTransportTLSModeSystem   = "system"
	GRPCTransportTLSModeInsecure = "insecure"
	GRPCTransportTLSModeCustomCA = "custom_ca"
	GRPCTransportTLSModeMTLS     = "mtls"

	GRPCExposureModeAuto     = "auto"
	GRPCExposureModeInline   = "inline"
	GRPCExposureModeDeferred = "deferred"

	DefaultGRPCDiscoveryRefreshTTLMS   = int((10 * time.Minute) / time.Millisecond)
	DefaultGRPCDialTimeoutMS           = 10_000
	DefaultGRPCReflectionTimeoutMS     = 5_000
	DefaultGRPCDeadlineMS              = 15_000
	DefaultGRPCMaxDeadlineMS           = 120_000
	MaxAllowedGRPCDeadlineMS           = int((1 * time.Hour) / time.Millisecond)
	DefaultGRPCMaxRecvMessageBytes     = 4 * 1024 * 1024
	DefaultGRPCExposureMode            = GRPCExposureModeAuto
	DefaultGRPCExposureDeferredTrigger = 25
)

// GRPCConfig contains named, user-approved gRPC endpoint profiles.
// Agents may only discover and call gRPC services through these profiles.
// An empty endpoints list is valid and intentionally disables gRPC tool exposure.
type GRPCConfig struct {
	Endpoints []GRPCEndpointConfig `json:"endpoints,omitempty" yaml:"endpoints,omitempty"`
}

// GRPCEndpointConfig describes one approved gRPC target and its policy.
type GRPCEndpointConfig struct {
	ID        string              `json:"id" yaml:"id"`
	Target    string              `json:"target" yaml:"target"`
	Discovery GRPCDiscoveryConfig `json:"discovery,omitempty" yaml:"discovery,omitempty"`
	Transport GRPCTransportConfig `json:"transport,omitempty" yaml:"transport,omitempty"`
	Auth      GRPCAuthConfig      `json:"auth,omitempty" yaml:"auth,omitempty"`
	Defaults  GRPCDefaultsConfig  `json:"defaults,omitempty" yaml:"defaults,omitempty"`
	Exposure  GRPCExposureConfig  `json:"exposure,omitempty" yaml:"exposure,omitempty"`
}

// GRPCDiscoveryConfig controls how service descriptors are loaded.
type GRPCDiscoveryConfig struct {
	Mode          string   `json:"mode,omitempty" yaml:"mode,omitempty"`
	RefreshTTL    string   `json:"refresh_ttl,omitempty" yaml:"refresh_ttl,omitempty"`
	DescriptorSet string   `json:"descriptor_set,omitempty" yaml:"descriptor_set,omitempty"`
	ProtoFiles    []string `json:"proto_files,omitempty" yaml:"proto_files,omitempty"`
	ImportPaths   []string `json:"import_paths,omitempty" yaml:"import_paths,omitempty"`
}

// GRPCTransportConfig centralizes TLS and connection security for a profile.
type GRPCTransportConfig struct {
	TLSMode    string `json:"tls_mode,omitempty" yaml:"tls_mode,omitempty"`
	CAFile     string `json:"ca_file,omitempty" yaml:"ca_file,omitempty"`
	CertFile   string `json:"cert_file,omitempty" yaml:"cert_file,omitempty"`
	KeyFile    string `json:"key_file,omitempty" yaml:"key_file,omitempty"`
	ServerName string `json:"server_name,omitempty" yaml:"server_name,omitempty"`
}

// GRPCAuthConfig contains profile-level metadata and safe per-call overrides.
// When allow_override_keys is empty, all per-call metadata overrides are rejected.
type GRPCAuthConfig struct {
	Metadata          map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	AllowOverrideKeys []string          `json:"allow_override_keys,omitempty" yaml:"allow_override_keys,omitempty"`
}

// GRPCDefaultsConfig bounds timeouts and message sizes for calls through a profile.
type GRPCDefaultsConfig struct {
	DialTimeoutMS       int `json:"dial_timeout_ms,omitempty" yaml:"dial_timeout_ms,omitempty"`
	ReflectionTimeoutMS int `json:"reflection_timeout_ms,omitempty" yaml:"reflection_timeout_ms,omitempty"`
	DeadlineMS          int `json:"deadline_ms,omitempty" yaml:"deadline_ms,omitempty"`
	MaxDeadlineMS       int `json:"max_deadline_ms,omitempty" yaml:"max_deadline_ms,omitempty"`
	MaxRecvMessageBytes int `json:"max_recv_message_bytes,omitempty" yaml:"max_recv_message_bytes,omitempty"`
}

// GRPCExposureConfig controls whether discovered tools are exposed inline or deferred.
type GRPCExposureConfig struct {
	Mode              string   `json:"mode,omitempty" yaml:"mode,omitempty"`
	DeferredThreshold int      `json:"deferred_threshold,omitempty" yaml:"deferred_threshold,omitempty"`
	Namespace         string   `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	IncludeServices   []string `json:"include_services,omitempty" yaml:"include_services,omitempty"`
	ExcludeMethods    []string `json:"exclude_methods,omitempty" yaml:"exclude_methods,omitempty"`
}

func (c GRPCConfig) Validate() []error {
	return ValidateGRPCConfig(c)
}

func (c GRPCDiscoveryConfig) EffectiveMode() string {
	mode := strings.ToLower(strings.TrimSpace(c.Mode))
	if mode == "" {
		return GRPCDiscoveryModeReflection
	}
	return mode
}

func (c GRPCDiscoveryConfig) EffectiveRefreshTTLMS() int {
	if strings.TrimSpace(c.RefreshTTL) == "" {
		return DefaultGRPCDiscoveryRefreshTTLMS
	}
	if d, err := time.ParseDuration(strings.TrimSpace(c.RefreshTTL)); err == nil && d >= 0 {
		return int(d / time.Millisecond)
	}
	return DefaultGRPCDiscoveryRefreshTTLMS
}

func (c GRPCTransportConfig) EffectiveTLSMode() string {
	mode := strings.ToLower(strings.TrimSpace(c.TLSMode))
	if mode == "" {
		return GRPCTransportTLSModeSystem
	}
	return mode
}

func (c GRPCDefaultsConfig) EffectiveDialTimeoutMS() int {
	if c.DialTimeoutMS > 0 {
		return c.DialTimeoutMS
	}
	return DefaultGRPCDialTimeoutMS
}

func (c GRPCDefaultsConfig) EffectiveReflectionTimeoutMS() int {
	if c.ReflectionTimeoutMS > 0 {
		return c.ReflectionTimeoutMS
	}
	return DefaultGRPCReflectionTimeoutMS
}

func (c GRPCDefaultsConfig) EffectiveDeadlineMS() int {
	if c.DeadlineMS > 0 {
		return c.DeadlineMS
	}
	return DefaultGRPCDeadlineMS
}

func (c GRPCDefaultsConfig) EffectiveMaxDeadlineMS() int {
	if c.MaxDeadlineMS > 0 {
		return c.MaxDeadlineMS
	}
	return DefaultGRPCMaxDeadlineMS
}

func (c GRPCDefaultsConfig) EffectiveMaxRecvMessageBytes() int {
	if c.MaxRecvMessageBytes > 0 {
		return c.MaxRecvMessageBytes
	}
	return DefaultGRPCMaxRecvMessageBytes
}

func (c GRPCExposureConfig) EffectiveMode() string {
	mode := strings.ToLower(strings.TrimSpace(c.Mode))
	if mode == "" {
		return DefaultGRPCExposureMode
	}
	return mode
}

func (c GRPCExposureConfig) EffectiveDeferredThreshold() int {
	if c.DeferredThreshold > 0 {
		return c.DeferredThreshold
	}
	return DefaultGRPCExposureDeferredTrigger
}

// ParseGRPCConfigBytes parses either a bare gRPC config document or a full
// config document containing a top-level grpc section. JSON/JSON5 and YAML are supported.
func ParseGRPCConfigBytes(raw []byte, extHint string) (GRPCConfig, error) {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(extHint)))
	var cfg GRPCConfig
	var wrapper struct {
		GRPC GRPCConfig `json:"grpc" yaml:"grpc"`
	}

	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return GRPCConfig{}, fmt.Errorf("parse gRPC YAML config: %w", err)
		}
		if err := yaml.Unmarshal(raw, &wrapper); err != nil {
			return GRPCConfig{}, fmt.Errorf("parse gRPC YAML wrapper: %w", err)
		}
	default:
		standardised, err := hujson.Standardize(raw)
		if err != nil {
			return GRPCConfig{}, fmt.Errorf("parse gRPC JSON5 config: %w", err)
		}
		if err := json.Unmarshal(standardised, &cfg); err != nil {
			return GRPCConfig{}, fmt.Errorf("parse gRPC JSON config: %w", err)
		}
		if err := json.Unmarshal(standardised, &wrapper); err != nil {
			return GRPCConfig{}, fmt.Errorf("parse gRPC JSON wrapper: %w", err)
		}
	}
	if len(cfg.Endpoints) == 0 && len(wrapper.GRPC.Endpoints) > 0 {
		cfg = wrapper.GRPC
	}
	return cfg, nil
}

func ValidateGRPCConfig(cfg GRPCConfig) []error {
	var errs []error
	seenIDs := map[string]struct{}{}
	for i, endpoint := range cfg.Endpoints {
		path := fmt.Sprintf("grpc.endpoints[%d]", i)
		id := strings.TrimSpace(endpoint.ID)
		if id == "" {
			errs = append(errs, fmt.Errorf("%s.id is required", path))
		} else if !validIdentifier(id, false) {
			errs = append(errs, fmt.Errorf("%s.id %q must contain only letters, digits, '-' or '_' and start with a letter or digit", path, endpoint.ID))
		} else {
			key := strings.ToLower(id)
			if _, exists := seenIDs[key]; exists {
				errs = append(errs, fmt.Errorf("%s.id %q duplicates another gRPC endpoint id", path, endpoint.ID))
			}
			seenIDs[key] = struct{}{}
		}
		if strings.TrimSpace(endpoint.Target) == "" {
			errs = append(errs, fmt.Errorf("%s.target is required", path))
		}
		errs = append(errs, validateGRPCDiscovery(path+".discovery", endpoint.Discovery)...)
		errs = append(errs, validateGRPCTransport(path+".transport", endpoint.Transport)...)
		errs = append(errs, validateGRPCAuth(path+".auth", endpoint.Auth)...)
		errs = append(errs, validateGRPCDefaults(path+".defaults", endpoint.Defaults)...)
		errs = append(errs, validateGRPCExposure(path+".exposure", endpoint.Exposure)...)
	}
	return errs
}

func validateGRPCDiscovery(path string, c GRPCDiscoveryConfig) []error {
	var errs []error
	mode := c.EffectiveMode()
	switch mode {
	case GRPCDiscoveryModeReflection:
		// Reflection is the safe default and needs no static descriptor inputs.
	case GRPCDiscoveryModeDescriptorSet:
		if strings.TrimSpace(c.DescriptorSet) == "" {
			errs = append(errs, fmt.Errorf("%s.descriptor_set is required when mode is descriptor_set", path))
		}
	case GRPCDiscoveryModeProtoFiles:
		if !hasNonEmptyString(c.ProtoFiles) {
			errs = append(errs, fmt.Errorf("%s.proto_files is required when mode is proto_files", path))
		}
	default:
		errs = append(errs, fmt.Errorf("%s.mode: unknown value %q (valid: reflection, descriptor_set, proto_files)", path, c.Mode))
	}
	if ttl := strings.TrimSpace(c.RefreshTTL); ttl != "" {
		d, err := time.ParseDuration(ttl)
		if err != nil || d < 0 {
			errs = append(errs, fmt.Errorf("%s.refresh_ttl must be a non-negative duration (got %q)", path, c.RefreshTTL))
		}
	}
	return errs
}

func validateGRPCTransport(path string, c GRPCTransportConfig) []error {
	var errs []error
	mode := c.EffectiveTLSMode()
	switch mode {
	case GRPCTransportTLSModeSystem, GRPCTransportTLSModeInsecure, GRPCTransportTLSModeCustomCA, GRPCTransportTLSModeMTLS:
	default:
		errs = append(errs, fmt.Errorf("%s.tls_mode: unknown value %q (valid: system, insecure, custom_ca, mtls)", path, c.TLSMode))
	}
	if mode == GRPCTransportTLSModeInsecure {
		if strings.TrimSpace(c.CAFile) != "" || strings.TrimSpace(c.CertFile) != "" || strings.TrimSpace(c.KeyFile) != "" || strings.TrimSpace(c.ServerName) != "" {
			errs = append(errs, fmt.Errorf("%s: tls_mode insecure cannot be combined with ca_file, cert_file, key_file, or server_name", path))
		}
	}
	if mode == GRPCTransportTLSModeCustomCA && strings.TrimSpace(c.CAFile) == "" {
		errs = append(errs, fmt.Errorf("%s.ca_file is required when tls_mode is custom_ca", path))
	}
	if mode == GRPCTransportTLSModeMTLS {
		if strings.TrimSpace(c.CertFile) == "" || strings.TrimSpace(c.KeyFile) == "" {
			errs = append(errs, fmt.Errorf("%s: tls_mode mtls requires both cert_file and key_file", path))
		}
	}
	if (strings.TrimSpace(c.CertFile) == "") != (strings.TrimSpace(c.KeyFile) == "") {
		errs = append(errs, fmt.Errorf("%s: cert_file and key_file must be provided together", path))
	}
	return errs
}

func validateGRPCAuth(path string, c GRPCAuthConfig) []error {
	var errs []error
	for key := range c.Metadata {
		if err := validateGRPCMetadataKey(key); err != nil {
			errs = append(errs, fmt.Errorf("%s.metadata[%q]: %w", path, key, err))
		}
	}
	seenOverrides := map[string]struct{}{}
	for i, key := range c.AllowOverrideKeys {
		if err := validateGRPCMetadataKey(key); err != nil {
			errs = append(errs, fmt.Errorf("%s.allow_override_keys[%d] %q: %w", path, i, key, err))
			continue
		}
		lower := strings.ToLower(strings.TrimSpace(key))
		if _, exists := seenOverrides[lower]; exists {
			errs = append(errs, fmt.Errorf("%s.allow_override_keys[%d] %q duplicates another override key", path, i, key))
		}
		seenOverrides[lower] = struct{}{}
	}
	return errs
}

func validateGRPCDefaults(path string, c GRPCDefaultsConfig) []error {
	var errs []error
	for name, value := range map[string]int{
		"dial_timeout_ms":        c.DialTimeoutMS,
		"reflection_timeout_ms":  c.ReflectionTimeoutMS,
		"deadline_ms":            c.DeadlineMS,
		"max_deadline_ms":        c.MaxDeadlineMS,
		"max_recv_message_bytes": c.MaxRecvMessageBytes,
	} {
		if value < 0 {
			errs = append(errs, fmt.Errorf("%s.%s must be >= 0 (got %d)", path, name, value))
		}
	}
	if c.EffectiveDeadlineMS() > c.EffectiveMaxDeadlineMS() {
		errs = append(errs, fmt.Errorf("%s.deadline_ms must be <= max_deadline_ms (got %d > %d)", path, c.EffectiveDeadlineMS(), c.EffectiveMaxDeadlineMS()))
	}
	if c.EffectiveDeadlineMS() > MaxAllowedGRPCDeadlineMS {
		errs = append(errs, fmt.Errorf("%s.deadline_ms must be <= %d (1h)", path, MaxAllowedGRPCDeadlineMS))
	}
	if c.EffectiveMaxDeadlineMS() > MaxAllowedGRPCDeadlineMS {
		errs = append(errs, fmt.Errorf("%s.max_deadline_ms must be <= %d (1h)", path, MaxAllowedGRPCDeadlineMS))
	}
	return errs
}

func validateGRPCExposure(path string, c GRPCExposureConfig) []error {
	var errs []error
	mode := c.EffectiveMode()
	switch mode {
	case GRPCExposureModeAuto, GRPCExposureModeInline, GRPCExposureModeDeferred:
	default:
		errs = append(errs, fmt.Errorf("%s.mode: unknown value %q (valid: auto, inline, deferred)", path, c.Mode))
	}
	if c.DeferredThreshold < 0 {
		errs = append(errs, fmt.Errorf("%s.deferred_threshold must be >= 0 (got %d)", path, c.DeferredThreshold))
	}
	if ns := strings.TrimSpace(c.Namespace); ns != "" && !validIdentifier(ns, true) {
		errs = append(errs, fmt.Errorf("%s.namespace %q must contain only letters, digits, or '_' and start with a letter or digit", path, c.Namespace))
	}
	for i, service := range c.IncludeServices {
		if strings.TrimSpace(service) == "" {
			errs = append(errs, fmt.Errorf("%s.include_services[%d] must not be empty", path, i))
		}
	}
	for i, method := range c.ExcludeMethods {
		trimmed := strings.TrimSpace(method)
		if trimmed == "" {
			errs = append(errs, fmt.Errorf("%s.exclude_methods[%d] must not be empty", path, i))
		} else if strings.Contains(trimmed, "/") && !strings.HasPrefix(trimmed, "/") {
			errs = append(errs, fmt.Errorf("%s.exclude_methods[%d] %q must use /package.Service/Method form when a slash is present", path, i, method))
		}
	}
	return errs
}

func validateGRPCConfigDocExtra(extra map[string]any) []error {
	if len(extra) == 0 {
		return nil
	}
	raw, ok := extra["grpc"]
	if !ok || raw == nil {
		return nil
	}
	var cfg GRPCConfig
	if !decodeAnyIntoStruct(raw, &cfg) {
		return []error{fmt.Errorf("grpc: invalid gRPC config shape")}
	}
	return ValidateGRPCConfig(cfg)
}

func detectUnknownGRPCKeys(raw any) []string {
	var errs []error
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	errs = appendStringErrors(errs, detectUnknownMapKeys("grpc", raw, []string{"endpoints"}))
	endpoints, ok := m["endpoints"].([]any)
	if !ok {
		return errorsToStrings(errs)
	}
	for i, item := range endpoints {
		epPath := fmt.Sprintf("grpc.endpoints[%d]", i)
		errs = appendStringErrors(errs, detectUnknownMapKeys(epPath, item, []string{"id", "target", "discovery", "transport", "auth", "defaults", "exposure"}))
		em, ok := item.(map[string]any)
		if !ok {
			continue
		}
		errs = appendStringErrors(errs, detectUnknownMapKeys(epPath+".discovery", em["discovery"], []string{"mode", "refresh_ttl", "descriptor_set", "proto_files", "import_paths"}))
		errs = appendStringErrors(errs, detectUnknownMapKeys(epPath+".transport", em["transport"], []string{"tls_mode", "ca_file", "cert_file", "key_file", "server_name"}))
		errs = appendStringErrors(errs, detectUnknownMapKeys(epPath+".auth", em["auth"], []string{"metadata", "allow_override_keys"}))
		errs = appendStringErrors(errs, detectUnknownMapKeys(epPath+".defaults", em["defaults"], []string{"dial_timeout_ms", "reflection_timeout_ms", "deadline_ms", "max_deadline_ms", "max_recv_message_bytes"}))
		errs = appendStringErrors(errs, detectUnknownMapKeys(epPath+".exposure", em["exposure"], []string{"mode", "deferred_threshold", "namespace", "include_services", "exclude_methods"}))
	}
	return errorsToStrings(errs)
}

func decodeAnyIntoStruct(raw any, out any) bool {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return false
	}
	return json.Unmarshal(encoded, out) == nil
}

func validateGRPCMetadataKey(key string) error {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return fmt.Errorf("metadata key is required")
	}
	if strings.HasPrefix(trimmed, ":") {
		return fmt.Errorf("pseudo-header metadata keys are not configurable")
	}
	if trimmed != strings.ToLower(trimmed) {
		return fmt.Errorf("metadata key must be lowercase")
	}
	if strings.HasPrefix(trimmed, "grpc-") {
		return fmt.Errorf("metadata key must not use reserved grpc- prefix")
	}
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("metadata key contains invalid character %q", r)
	}
	return nil
}

func validIdentifier(value string, underscoreOnly bool) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for i, r := range value {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
		if !underscoreOnly {
			valid = valid || r == '-'
		}
		if !valid {
			return false
		}
		if i == 0 && !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func hasNonEmptyString(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func appendStringErrors(errs []error, values []string) []error {
	for _, value := range values {
		errs = append(errs, fmt.Errorf("%s", value))
	}
	return errs
}

func errorsToStrings(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, err := range errs {
		out = append(out, err.Error())
	}
	return out
}
