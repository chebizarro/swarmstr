package context

// ContextEngineRuntimeMode describes whether the host is running normally or in a fallback/degraded path.
type ContextEngineRuntimeMode string

const (
	ContextEngineRuntimeNormal   ContextEngineRuntimeMode = "normal"
	ContextEngineRuntimeFallback ContextEngineRuntimeMode = "fallback"
	ContextEngineRuntimeDegraded ContextEngineRuntimeMode = "degraded"
)

// ContextEngineSelectionSource describes how the active engine was selected.
type ContextEngineSelectionSource string

const (
	ContextEngineSelectionConfigured ContextEngineSelectionSource = "configured"
	ContextEngineSelectionDefault    ContextEngineSelectionSource = "default"
	ContextEngineSelectionUnknown    ContextEngineSelectionSource = "unknown"
)

// ContextEngineRuntimeReasonCode normalizes fallback/degraded diagnostics.
type ContextEngineRuntimeReasonCode string

const (
	ContextEngineReasonProviderTimeout     ContextEngineRuntimeReasonCode = "provider_timeout"
	ContextEngineReasonProviderUnavailable ContextEngineRuntimeReasonCode = "provider_unavailable"
	ContextEngineReasonRateLimited         ContextEngineRuntimeReasonCode = "rate_limited"
	ContextEngineReasonContextOverflow     ContextEngineRuntimeReasonCode = "context_overflow"
	ContextEngineReasonRuntimeUnavailable  ContextEngineRuntimeReasonCode = "runtime_unavailable"
	ContextEngineReasonUnknown             ContextEngineRuntimeReasonCode = "unknown"
)

// ContextEngineHostCapability advertises host features optional engines may require.
type ContextEngineHostCapability string

const (
	HostCapabilityBootstrap                 ContextEngineHostCapability = "bootstrap"
	HostCapabilityAssembleBeforePrompt      ContextEngineHostCapability = "assemble-before-prompt"
	HostCapabilityAfterTurn                 ContextEngineHostCapability = "after-turn"
	HostCapabilityMaintain                  ContextEngineHostCapability = "maintain"
	HostCapabilityCompact                   ContextEngineHostCapability = "compact"
	HostCapabilityRuntimeLLMComplete        ContextEngineHostCapability = "runtime-llm-complete"
	HostCapabilityThreadBootstrapProjection ContextEngineHostCapability = "thread-bootstrap-projection"
)

// ContextEngineRuntimeSettings carries host/model/runtime limits without tying engines to runner packages.
type ContextEngineRuntimeSettings struct {
	SchemaVersion int `json:"schema_version"`
	Runtime       struct {
		Host      string                   `json:"host"`
		Mode      ContextEngineRuntimeMode `json:"mode"`
		HarnessID string                   `json:"harness_id,omitempty"`
		RuntimeID string                   `json:"runtime_id,omitempty"`
	} `json:"runtime"`
	Model struct {
		Requested string `json:"requested,omitempty"`
		Resolved  string `json:"resolved,omitempty"`
		Provider  string `json:"provider,omitempty"`
		Family    string `json:"family,omitempty"`
	} `json:"model"`
	ContextEngineSelection struct {
		SelectedID string                       `json:"selected_id,omitempty"`
		Source     ContextEngineSelectionSource `json:"source"`
	} `json:"context_engine_selection"`
	ExecutionHost struct {
		ID    string `json:"id,omitempty"`
		Label string `json:"label,omitempty"`
	} `json:"execution_host"`
	Limits struct {
		PromptTokenBudget int `json:"prompt_token_budget,omitempty"`
		MaxOutputTokens   int `json:"max_output_tokens,omitempty"`
	} `json:"limits"`
	Diagnostics struct {
		FallbackReason ContextEngineRuntimeReasonCode `json:"fallback_reason,omitempty"`
		DegradedReason ContextEngineRuntimeReasonCode `json:"degraded_reason,omitempty"`
	} `json:"diagnostics"`
}

// RuntimeSettingsProvider is implemented by engines that expose runtime settings.
type RuntimeSettingsProvider interface {
	RuntimeSettings() ContextEngineRuntimeSettings
}

// HostCapabilitiesProvider is implemented by engines that declare required host capabilities.
type HostCapabilitiesProvider interface {
	RequiredHostCapabilities(operation string) []ContextEngineHostCapability
}

// DefaultContextEngineRuntimeSettings returns normalized zero/default runtime settings.
func DefaultContextEngineRuntimeSettings() ContextEngineRuntimeSettings {
	var settings ContextEngineRuntimeSettings
	settings.SchemaVersion = 1
	settings.Runtime.Host = "swarmstr"
	settings.Runtime.Mode = ContextEngineRuntimeNormal
	settings.ContextEngineSelection.Source = ContextEngineSelectionUnknown
	return settings
}

// ResolveRuntimeSettings returns engine-provided runtime settings or defaults.
func ResolveRuntimeSettings(engine Engine) ContextEngineRuntimeSettings {
	if provider, ok := engine.(RuntimeSettingsProvider); ok {
		settings := provider.RuntimeSettings()
		if settings.SchemaVersion == 0 {
			settings.SchemaVersion = 1
		}
		if settings.Runtime.Mode == "" {
			settings.Runtime.Mode = ContextEngineRuntimeNormal
		}
		if settings.ContextEngineSelection.Source == "" {
			settings.ContextEngineSelection.Source = ContextEngineSelectionUnknown
		}
		return settings
	}
	return DefaultContextEngineRuntimeSettings()
}
