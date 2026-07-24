package main

import (
	"log"
	"strings"

	configpkg "metiq/internal/config"
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
	if strings.TrimSpace(req.Driver) != "" && !sandboxRequestOverrideLocked(daemonCfg, "driver") {
		cfg.Driver = strings.ToLower(strings.TrimSpace(req.Driver))
	}
	if req.TimeoutSeconds > 0 && !sandboxRequestOverrideLocked(daemonCfg, "timeout_s") {
		cfg.TimeoutSeconds = req.TimeoutSeconds
	}
	// A request may narrow the daemon's unsafe-nop opt-in, but can never enable
	// host execution on its own.
	if req.AllowUnsafeNop != nil && !sandboxRequestOverrideLocked(daemonCfg, "allow_unsafe_nop") {
		cfg.AllowUnsafeNop = cfg.AllowUnsafeNop && *req.AllowUnsafeNop
	}
	if req.MemoryLimit != "" && !sandboxRequestOverrideLocked(daemonCfg, "memory_limit") {
		cfg.MemoryLimit = strings.TrimSpace(req.MemoryLimit)
	}
	if req.CPULimit != "" && !sandboxRequestOverrideLocked(daemonCfg, "cpu_limit") {
		cfg.CPULimit = strings.TrimSpace(req.CPULimit)
	}
	if req.DockerImage != "" && !sandboxRequestOverrideLocked(daemonCfg, "docker_image") {
		cfg.DockerImage = strings.TrimSpace(req.DockerImage)
	}
	if req.NetworkDisabled != nil && !sandboxRequestOverrideLocked(daemonCfg, "network_disabled") {
		cfg.NetworkDisabled = *req.NetworkDisabled
	}
	if req.AllowNetwork != nil && !sandboxRequestOverrideLocked(daemonCfg, "allow_network") {
		cfg.AllowNetwork = *req.AllowNetwork
	}
	if req.AllowedDomains != nil && !sandboxRequestOverrideLocked(daemonCfg, "allowed_domains") {
		cfg.AllowedDomains = cleanCfgStrings(req.AllowedDomains)
	}
	if req.AllowedCIDRs != nil && !sandboxRequestOverrideLocked(daemonCfg, "allowed_cidrs") {
		cfg.AllowedCIDRs = cleanCfgStrings(req.AllowedCIDRs)
	}
	if req.EgressEnforced != nil && !sandboxRequestOverrideLocked(daemonCfg, "egress_enforced") {
		cfg.EgressEnforced = *req.EgressEnforced
	}
	if req.ReadOnlyRootFS != nil && !sandboxRequestOverrideLocked(daemonCfg, "read_only_rootfs") {
		cfg.ReadOnlyRootFS = *req.ReadOnlyRootFS
	}
	if req.WritableRootFS != nil && !sandboxRequestOverrideLocked(daemonCfg, "writable_rootfs") {
		cfg.WritableRootFS = *req.WritableRootFS
	}
	if req.CapDrop != nil && !sandboxRequestOverrideLocked(daemonCfg, "cap_drop") {
		cfg.CapDrop = cleanCfgStrings(req.CapDrop)
	}
	if req.SecurityOpt != nil && !sandboxRequestOverrideLocked(daemonCfg, "security_opt") {
		cfg.SecurityOpt = cleanCfgStrings(req.SecurityOpt)
	}
	if req.PidsLimit > 0 && !sandboxRequestOverrideLocked(daemonCfg, "pids_limit") {
		cfg.PidsLimit = req.PidsLimit
	}
	if req.User != "" && !sandboxRequestOverrideLocked(daemonCfg, "user") {
		cfg.User = strings.TrimSpace(req.User)
	}
	if req.Tmpfs != nil && !sandboxRequestOverrideLocked(daemonCfg, "tmpfs") {
		cfg.Tmpfs = cleanCfgStrings(req.Tmpfs)
	}
	if req.Ulimits != nil && !sandboxRequestOverrideLocked(daemonCfg, "ulimits") {
		cfg.Ulimits = cleanCfgStrings(req.Ulimits)
	}
	if req.MaxOutputBytes > 0 && !sandboxRequestOverrideLocked(daemonCfg, "max_output_bytes") {
		cfg.MaxOutputBytes = req.MaxOutputBytes
	}
	if req.WorkspaceDir != "" && !sandboxRequestOverrideLocked(daemonCfg, "workspace_dir") {
		cfg.WorkspaceDir = strings.TrimSpace(req.WorkspaceDir)
	}
	if req.ContainerWorkdir != "" && !sandboxRequestOverrideLocked(daemonCfg, "container_workdir") {
		cfg.ContainerWorkdir = strings.TrimSpace(req.ContainerWorkdir)
	}
	if req.WorkspaceAccess != "" && !sandboxRequestOverrideLocked(daemonCfg, "workspace_access") {
		cfg.WorkspaceAccess = strings.TrimSpace(req.WorkspaceAccess)
	}
	if req.PersistentRuntime != nil && !sandboxRequestOverrideLocked(daemonCfg, "persistent_runtime") {
		cfg.PersistentRuntime = *req.PersistentRuntime
	}
	if req.RuntimeScope != "" && !sandboxRequestOverrideLocked(daemonCfg, "runtime_scope") {
		cfg.RuntimeScope = strings.TrimSpace(req.RuntimeScope)
	}
	if req.RuntimeKey != "" && !sandboxRequestOverrideLocked(daemonCfg, "runtime_key") {
		cfg.RuntimeKey = strings.TrimSpace(req.RuntimeKey)
	}
	return cfg, configuredDriver
}

func sandboxRequestOverrideLocked(daemonCfg state.ConfigDoc, field string) bool {
	managed, ok := configpkg.ManagedSettingsFromConfig(daemonCfg)
	if !ok {
		return false
	}
	target := "extra.sandbox." + strings.TrimSpace(field)
	for _, lockedPath := range managed.LockedPaths {
		lockedPath = strings.Trim(strings.TrimSpace(lockedPath), ".")
		if lockedPath == "extra.sandbox" || lockedPath == target || strings.HasPrefix(target, lockedPath+".") {
			log.Printf("managed settings ignored sandbox.run override for locked path %s", target)
			return true
		}
	}
	return false
}

func sandboxConfigFromMap(m map[string]any) sandbox.Config {
	cfg := sandbox.Config{
		Driver:            getCfgString(m, "driver"),
		AllowUnsafeNop:    getCfgBool(m, "allow_unsafe_nop"),
		MemoryLimit:       getCfgString(m, "memory_limit"),
		CPULimit:          getCfgString(m, "cpu_limit"),
		DockerImage:       getCfgString(m, "docker_image"),
		NetworkDisabled:   getCfgBool(m, "network_disabled"),
		AllowNetwork:      getCfgBool(m, "allow_network"),
		AllowedDomains:    firstCfgStringSlice(m, "allowed_domains", "egress_allowed_domains"),
		AllowedCIDRs:      firstCfgStringSlice(m, "allowed_cidrs", "egress_allowed_cidrs"),
		EgressEnforced:    getCfgBool(m, "egress_enforced"),
		ReadOnlyRootFS:    getCfgBool(m, "read_only_rootfs"),
		WritableRootFS:    getCfgBool(m, "writable_rootfs"),
		CapDrop:           getCfgStringSlice(m, "cap_drop"),
		SecurityOpt:       getCfgStringSlice(m, "security_opt"),
		User:              getCfgString(m, "user"),
		Tmpfs:             getCfgStringSlice(m, "tmpfs"),
		Ulimits:           getCfgStringSlice(m, "ulimits"),
		WorkspaceDir:      firstCfgString(m, "workspace_dir", "workspace_mount", "workspace"),
		ContainerWorkdir:  firstCfgString(m, "container_workdir", "workspace_target"),
		WorkspaceAccess:   getCfgString(m, "workspace_access"),
		PersistentRuntime: getCfgBool(m, "persistent_runtime"),
		RuntimeScope:      getCfgString(m, "runtime_scope"),
		RuntimeKey:        getCfgString(m, "runtime_key"),
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
