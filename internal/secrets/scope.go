package secrets

import (
	"fmt"
	"path"
	"strings"
)

// TargetRule constrains which secret references may appear at a config path.
type TargetRule struct {
	PathPattern string   `json:"path_pattern"`
	AllowedRefs []string `json:"allowed_refs,omitempty"`
	Required    bool     `json:"required,omitempty"`
}

// TargetRegistry maps config fields to allowed secret references.
type TargetRegistry struct {
	Rules []TargetRule `json:"rules"`
}

// Validate checks a config path/value pair against the registry. Plain values in
// registered paths are rejected; refs not matching AllowedRefs are rejected.
func (r TargetRegistry) Validate(configPath, value string) error {
	configPath = strings.TrimSpace(configPath)
	for _, rule := range r.Rules {
		if !glob(rule.PathPattern, configPath) {
			continue
		}
		refs := SecretRefs(value)
		if len(refs) == 0 {
			return fmt.Errorf("secret target %s requires a secret reference", configPath)
		}
		for _, ref := range refs {
			if !allowedRef(rule.AllowedRefs, ref) {
				return fmt.Errorf("secret reference %s is not allowed at %s", ref, configPath)
			}
		}
		return nil
	}
	return nil
}

// SecretRefs extracts $VAR, ${VAR}, env:VAR, and secret:VAR style references.
func SecretRefs(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == '"' || r == '\''
	})
	var refs []string
	for _, field := range fields {
		field = strings.Trim(field, "{}[]()")
		if strings.HasPrefix(field, "env:") || strings.HasPrefix(field, "secret:") || strings.HasPrefix(field, "$secret:") {
			refs = append(refs, strings.TrimPrefix(field, "$"))
			continue
		}
		if strings.HasPrefix(field, "${") && strings.HasSuffix(field, "}") {
			refs = append(refs, "$"+strings.TrimSuffix(strings.TrimPrefix(field, "${"), "}"))
			continue
		}
		if strings.HasPrefix(field, "$") && len(field) > 1 {
			refs = append(refs, field)
		}
	}
	return refs
}

func allowedRef(allowed []string, ref string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, pattern := range allowed {
		if glob(pattern, ref) {
			return true
		}
	}
	return false
}

func glob(pattern, value string) bool {
	if strings.TrimSpace(pattern) == "" {
		return false
	}
	if pattern == "*" || pattern == value {
		return true
	}
	ok, err := path.Match(pattern, value)
	return err == nil && ok
}

func looksSensitivePath(configPath string) bool {
	lower := strings.ToLower(configPath)
	for _, token := range []string{"secret", "token", "password", "private_key", "api_key", "credential"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}
