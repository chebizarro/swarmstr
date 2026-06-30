package main

import (
	"strings"

	"metiq/internal/gateway/methods"
	"metiq/internal/sandbox"
	"metiq/internal/store/state"
)

func sandboxConfigFromDaemonAndRequest(daemonCfg state.ConfigDoc, req methods.SandboxRunRequest) (sandbox.Config, string) {
	cfg := sandbox.Config{}
	if daemonCfg.Extra != nil {
		if rawSandbox, ok := daemonCfg.Extra["sandbox"].(map[string]any); ok {
			cfg = sandboxConfigFromMap(rawSandbox)
		}
	}
	configuredDriver := strings.ToLower(strings.TrimSpace(cfg.Driver))
	if strings.TrimSpace(req.Driver) != "" {
		cfg.Driver = strings.ToLower(strings.TrimSpace(req.Driver))
	}
	if req.TimeoutSeconds > 0 {
		cfg.TimeoutSeconds = req.TimeoutSeconds
	}
	return cfg, configuredDriver
}

func sandboxConfigFromMap(m map[string]any) sandbox.Config {
	cfg := sandbox.Config{
		Driver:           getCfgString(m, "driver"),
		AllowUnsafeNop:   getCfgBool(m, "allow_unsafe_nop"),
		MemoryLimit:      getCfgString(m, "memory_limit"),
		CPULimit:         getCfgString(m, "cpu_limit"),
		DockerImage:      getCfgString(m, "docker_image"),
		NetworkDisabled:  getCfgBool(m, "network_disabled"),
		AllowNetwork:     getCfgBool(m, "allow_network"),
		AllowedDomains:   firstCfgStringSlice(m, "allowed_domains", "egress_allowed_domains"),
		AllowedCIDRs:     firstCfgStringSlice(m, "allowed_cidrs", "egress_allowed_cidrs"),
		EgressEnforced:   getCfgBool(m, "egress_enforced"),
		ReadOnlyRootFS:   getCfgBool(m, "read_only_rootfs"),
		WritableRootFS:   getCfgBool(m, "writable_rootfs"),
		CapDrop:          getCfgStringSlice(m, "cap_drop"),
		SecurityOpt:      getCfgStringSlice(m, "security_opt"),
		User:             getCfgString(m, "user"),
		Tmpfs:            getCfgStringSlice(m, "tmpfs"),
		Ulimits:          getCfgStringSlice(m, "ulimits"),
		WorkspaceDir:     firstCfgString(m, "workspace_dir", "workspace_mount", "workspace"),
		ContainerWorkdir: firstCfgString(m, "container_workdir", "workspace_target"),
		WorkspaceAccess:  getCfgString(m, "workspace_access"),
	}
	if n, ok := cfgInt(m["timeout_s"]); ok {
		cfg.TimeoutSeconds = n
	}
	if n, ok := cfgInt(m["pids_limit"]); ok {
		cfg.PidsLimit = n
	}
	if n, ok := cfgInt64(m["max_output_bytes"]); ok {
		cfg.MaxOutputBytes = n
	}
	return cfg
}

func firstCfgString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if s := getCfgString(m, key); s != "" {
			return s
		}
	}
	return ""
}

func getCfgString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func getCfgBool(m map[string]any, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func firstCfgStringSlice(m map[string]any, keys ...string) []string {
	for _, key := range keys {
		if v := getCfgStringSlice(m, key); len(v) > 0 {
			return v
		}
	}
	return nil
}

func getCfgStringSlice(m map[string]any, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch x := v.(type) {
	case []string:
		return cleanCfgStrings(x)
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return cleanCfgStrings(out)
	case string:
		return cleanCfgStrings([]string{x})
	default:
		return nil
	}
}

func cleanCfgStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if s := strings.TrimSpace(value); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func cfgInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func cfgInt64(v any) (int64, bool) {
	if n, ok := cfgInt(v); ok {
		return int64(n), true
	}
	return 0, false
}
