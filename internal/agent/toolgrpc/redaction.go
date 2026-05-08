package toolgrpc

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const redactedValue = "[REDACTED]"

var secretValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)basic\s+[a-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)secret:[^\s,}\]]+`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*(PRIVATE KEY|CERTIFICATE)-----[\s\S]*?-----END [A-Z ]*(PRIVATE KEY|CERTIFICATE)-----`),
}

// Redactor removes gRPC auth, metadata, and TLS material before data reaches
// tool results, lifecycle events, traces, or hook payloads.
type Redactor struct{}

func NewRedactor() Redactor { return Redactor{} }

func (r Redactor) RedactError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", r.RedactString(err.Error()))
}

func (r Redactor) RedactString(value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err == nil {
		redacted := r.RedactValue(decoded)
		if encoded, err := json.Marshal(redacted); err == nil {
			return string(encoded)
		}
	}
	out := value
	for _, pattern := range secretValuePatterns {
		out = pattern.ReplaceAllString(out, redactedValue)
	}
	return out
}

func (r Redactor) RedactJSONBytes(raw []byte) ([]byte, error) {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return json.Marshal(r.RedactValue(decoded))
}

func (r Redactor) RedactMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		if sensitiveMetadataKey(key) || sensitiveStringValue(value) {
			out[key] = redactedValue
			continue
		}
		out[key] = value
	}
	return out
}

func (r Redactor) RedactValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if sensitiveFieldKey(key) {
				out[key] = redactedValue
				continue
			}
			out[key] = r.RedactValue(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = r.RedactValue(item)
		}
		return out
	case string:
		if sensitiveStringValue(v) {
			return redactedValue
		}
		return r.RedactString(v)
	default:
		return value
	}
}

func sensitiveFieldKey(key string) bool {
	k := normalizeRedactionKey(key)
	if sensitiveMetadataKey(k) {
		return true
	}
	switch k {
	case "ca_file", "cert_file", "certificate", "client_certificate", "key_file", "private_key", "tls_cert", "tls_key", "token", "access_token", "refresh_token", "api_key", "apikey", "password", "secret":
		return true
	default:
		return strings.Contains(k, "private_key") || strings.Contains(k, "password") || strings.Contains(k, "secret") || strings.HasSuffix(k, "_token")
	}
}

func sensitiveMetadataKey(key string) bool {
	k := normalizeRedactionKey(key)
	if strings.HasSuffix(k, "_bin") {
		return true
	}
	switch k {
	case "authorization", "proxy_authorization", "x_api_key", "api_key", "apikey", "x_auth_token", "x_access_token", "cookie", "set_cookie":
		return true
	default:
		return strings.Contains(k, "token") || strings.Contains(k, "secret") || strings.Contains(k, "password") || strings.Contains(k, "credential")
	}
}

func sensitiveStringValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	for _, pattern := range secretValuePatterns {
		if pattern.MatchString(trimmed) {
			return true
		}
	}
	return false
}

func normalizeRedactionKey(key string) string {
	key = strings.TrimSpace(strings.ToLower(key))
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, ".", "_")
	return key
}
