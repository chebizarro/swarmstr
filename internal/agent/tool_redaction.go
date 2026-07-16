package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const RedactedToolValue = "[REDACTED]"

var toolSecretValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)basic\s+[a-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)secret:[^\s,}\]]+`),
	regexp.MustCompile(`(?i)\b(?:macaroon|payment[_-]?preimage|preimage|authorization|l402|lsat|token|access[_-]?token|refresh[_-]?token|api[_-]?key|password)\s*[:=]\s*["']?[a-z0-9._~+/=:-]+["']?`),
	regexp.MustCompile(`(?i)\b(?:l402|lsat)\s+[a-z0-9._~+/=:-]+`),
	regexp.MustCompile(`(?i)\b[a-z0-9._~+/=-]{16,}:[a-f0-9]{64}\b`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*(PRIVATE KEY|CERTIFICATE)-----[\s\S]*?-----END [A-Z ]*(PRIVATE KEY|CERTIFICATE)-----`),
}

// ToolRedactor removes credentials and payment secrets from copies of tool data
// before those copies cross persistence, trace, lifecycle, or plugin-hook
// boundaries. It never mutates the supplied value.
type ToolRedactor struct{}

func NewToolRedactor() ToolRedactor { return ToolRedactor{} }

func (r ToolRedactor) RedactError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", r.RedactString(err.Error()))
}

func (r ToolRedactor) RedactString(value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err == nil {
		switch decoded.(type) {
		case map[string]any, []any:
			if encoded, err := json.Marshal(r.RedactValue(decoded)); err == nil {
				return string(encoded)
			}
		}
	}
	out := value
	for _, pattern := range toolSecretValuePatterns {
		out = pattern.ReplaceAllString(out, RedactedToolValue)
	}
	return out
}

func (r ToolRedactor) RedactJSONBytes(raw []byte) ([]byte, error) {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return json.Marshal(r.RedactValue(decoded))
}

func (r ToolRedactor) RedactMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		if sensitiveToolMetadataKey(key) || sensitiveToolStringValue(value) {
			out[key] = RedactedToolValue
			continue
		}
		out[key] = value
	}
	return out
}

func (r ToolRedactor) RedactValue(value any) any {
	return r.redactValue(value, nil)
}

// RedactToolCall returns a persistence-safe copy of call. Descriptor schemas are
// honored when they mark fields writeOnly/x-sensitive or use a secret format;
// key-based matching remains the fallback for built-in and protobuf schemas.
func (r ToolRedactor) RedactToolCall(call ToolCall, descriptor ToolDescriptor) ToolCall {
	out := call
	if len(call.Args) == 0 {
		return out
	}
	schema := descriptor.InputJSONSchema
	if len(schema) == 0 && (descriptor.Parameters.Type != "" || len(descriptor.Parameters.Properties) > 0) {
		schema = toolInputSchemaMap(descriptor.Definition())
	}
	if redacted, ok := r.redactValue(call.Args, schema).(map[string]any); ok {
		out.Args = redacted
	}
	return out
}

func (r ToolRedactor) redactValue(value any, schema map[string]any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		properties, _ := schema["properties"].(map[string]any)
		additional, _ := schema["additionalProperties"].(map[string]any)
		for key, item := range v {
			childSchema, _ := properties[key].(map[string]any)
			if childSchema == nil {
				childSchema = additional
			}
			if sensitiveToolFieldKey(key) || sensitiveToolSchema(childSchema) {
				out[key] = RedactedToolValue
				continue
			}
			out[key] = r.redactValue(item, childSchema)
		}
		return out
	case []any:
		out := make([]any, len(v))
		itemSchema, _ := schema["items"].(map[string]any)
		for i, item := range v {
			out[i] = r.redactValue(item, itemSchema)
		}
		return out
	case string:
		if sensitiveToolStringValue(v) {
			return RedactedToolValue
		}
		return r.RedactString(v)
	default:
		return value
	}
}

func sensitiveToolSchema(schema map[string]any) bool {
	if len(schema) == 0 {
		return false
	}
	for _, key := range []string{"writeOnly", "x-sensitive", "x-secret"} {
		if marked, _ := schema[key].(bool); marked {
			return true
		}
	}
	format, _ := schema["format"].(string)
	switch normalizeToolRedactionKey(format) {
	case "password", "secret", "token", "credential", "authorization", "macaroon", "preimage", "invoice", "payment_request", "l402", "lsat":
		return true
	default:
		return false
	}
}

func sensitiveToolFieldKey(key string) bool {
	k := normalizeToolRedactionKey(key)
	if sensitiveToolMetadataKey(k) {
		return true
	}
	switch k {
	case "ca_file", "cert_file", "certificate", "client_certificate", "key_file", "private_key", "tls_cert", "tls_key",
		"token", "access_token", "refresh_token", "api_key", "apikey", "password", "secret",
		"macaroon", "payment_preimage", "preimage", "l402", "lsat", "authorization",
		"invoice", "payment_request", "paymentrequest", "pay_req", "payreq", "bolt11", "bolt_11":
		return true
	default:
		return strings.Contains(k, "private_key") ||
			strings.Contains(k, "password") ||
			strings.Contains(k, "secret") ||
			strings.HasSuffix(k, "_token")
	}
}

func sensitiveToolMetadataKey(key string) bool {
	k := normalizeToolRedactionKey(key)
	if strings.HasSuffix(k, "_bin") {
		return true
	}
	switch k {
	case "authorization", "proxy_authorization", "x_api_key", "api_key", "apikey", "x_auth_token", "x_access_token",
		"cookie", "set_cookie", "macaroon", "payment_preimage", "preimage", "l402", "lsat":
		return true
	default:
		return strings.Contains(k, "token") ||
			strings.Contains(k, "secret") ||
			strings.Contains(k, "password") ||
			strings.Contains(k, "credential")
	}
}

func sensitiveToolStringValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	for _, pattern := range toolSecretValuePatterns {
		if pattern.MatchString(trimmed) {
			return true
		}
	}
	return false
}

func normalizeToolRedactionKey(key string) string {
	key = strings.TrimSpace(strings.ToLower(key))
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, ".", "_")
	return key
}

// RedactToolCallForPersistence resolves the call's canonical descriptor when
// available and returns a redacted copy suitable for durable or hook payloads.
func RedactToolCallForPersistence(executor ToolExecutor, call ToolCall) ToolCall {
	descriptor, _ := ToolDescriptorForExecutor(executor, call.Name)
	return NewToolRedactor().RedactToolCall(call, descriptor)
}

// ToolDescriptorForExecutor resolves the immutable per-turn descriptor carried
// by registry and executor snapshots.
func ToolDescriptorForExecutor(executor ToolExecutor, name string) (ToolDescriptor, bool) {
	if resolver, ok := executor.(interface {
		Descriptor(string) (ToolDescriptor, bool)
	}); ok {
		return resolver.Descriptor(name)
	}
	return ToolDescriptor{}, false
}

// ToolCallRefForPersistence serializes only the redacted copy of a tool call.
func ToolCallRefForPersistence(executor ToolExecutor, call ToolCall) ToolCallRef {
	safeCall := RedactToolCallForPersistence(executor, call)
	ref := ToolCallRef{ID: safeCall.ID, Name: safeCall.Name}
	if len(safeCall.Args) > 0 {
		if encoded, err := json.Marshal(safeCall.Args); err == nil {
			ref.ArgsJSON = string(encoded)
		}
	}
	return ref
}

// RedactConversationMessagesForPersistence returns a copy with every serialized
// tool-call argument object passed through the same centralized redactor.
func RedactConversationMessagesForPersistence(executor ToolExecutor, messages []ConversationMessage) []ConversationMessage {
	if len(messages) == 0 {
		return messages
	}
	out := make([]ConversationMessage, len(messages))
	copy(out, messages)
	for i := range out {
		if len(messages[i].ToolCalls) == 0 {
			continue
		}
		out[i].ToolCalls = make([]ToolCallRef, len(messages[i].ToolCalls))
		for j, ref := range messages[i].ToolCalls {
			safeRef := ref
			if strings.TrimSpace(ref.ArgsJSON) != "" {
				var args map[string]any
				if err := json.Unmarshal([]byte(ref.ArgsJSON), &args); err == nil {
					safeRef = ToolCallRefForPersistence(executor, ToolCall{ID: ref.ID, Name: ref.Name, Args: args})
				} else {
					safeRef.ArgsJSON = NewToolRedactor().RedactString(ref.ArgsJSON)
				}
			}
			out[i].ToolCalls[j] = safeRef
		}
	}
	return out
}

// RestoreRedactedToolArgs overlays a hook's safe mutation result onto the
// original arguments. Unchanged redaction placeholders are replaced with the
// original secret so hooks cannot accidentally corrupt actual tool execution.
func RestoreRedactedToolArgs(original, mutated map[string]any) map[string]any {
	if mutated == nil {
		return nil
	}
	restored, _ := restoreRedactedToolValue(original, mutated).(map[string]any)
	return restored
}

func restoreRedactedToolValue(original, mutated any) any {
	if text, ok := mutated.(string); ok && text == RedactedToolValue {
		return cloneToolRedactionValue(original)
	}
	switch next := mutated.(type) {
	case map[string]any:
		prior, _ := original.(map[string]any)
		out := make(map[string]any, len(next))
		for key, item := range next {
			out[key] = restoreRedactedToolValue(prior[key], item)
		}
		return out
	case []any:
		prior, _ := original.([]any)
		out := make([]any, len(next))
		for i, item := range next {
			var previous any
			if i < len(prior) {
				previous = prior[i]
			}
			out[i] = restoreRedactedToolValue(previous, item)
		}
		return out
	default:
		return cloneToolRedactionValue(mutated)
	}
}

func cloneToolRedactionValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = cloneToolRedactionValue(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = cloneToolRedactionValue(item)
		}
		return out
	default:
		return value
	}
}
