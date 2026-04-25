// Package manifest defines the typed plugin manifest contract for metiq plugins.
//
// This package provides:
//   - Versioned manifest schema with explicit capability declarations
//   - Validation at publish, install, and load time
//   - Runtime compatibility checks
//   - Plugin metadata for CLI, API, and operator policy
//
// Plugin authors declare their capabilities in the manifest, and the runtime
// validates and registers these capabilities appropriately.
package manifest

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ─── Schema Version ──────────────────────────────────────────────────────────

// SchemaVersion is the current manifest schema version.
// Increment this when making breaking changes to the manifest structure.
const SchemaVersion = 2

// MinSupportedVersion is the minimum manifest version the runtime supports.
const MinSupportedVersion = 1

// ─── Manifest ────────────────────────────────────────────────────────────────

// Manifest is the top-level plugin manifest structure.
// Version 2 adds explicit capability declarations and runtime constraints.
type Manifest struct {
	// SchemaVersion is the manifest schema version (required, must be >= 1).
	SchemaVersion int `json:"schema_version"`

	// ─── Identity ────────────────────────────────────────────────────────────

	// ID is the unique plugin identifier (required).
	// Must be lowercase alphanumeric with hyphens, e.g. "my-plugin".
	ID string `json:"id"`

	// Version is the semantic version string (required), e.g. "1.2.3".
	Version string `json:"version"`

	// Name is a human-readable display name (optional).
	Name string `json:"name,omitempty"`

	// Description is a brief description of what the plugin does (optional).
	Description string `json:"description,omitempty"`

	// Author identifies the plugin author (optional).
	Author *AuthorInfo `json:"author,omitempty"`

	// License is an SPDX license identifier (optional), e.g. "MIT", "Apache-2.0".
	License string `json:"license,omitempty"`

	// Homepage is a URL for more information (optional).
	Homepage string `json:"homepage,omitempty"`

	// Repository is the source code repository URL (optional).
	Repository string `json:"repository,omitempty"`

	// ─── Runtime ─────────────────────────────────────────────────────────────

	// Runtime specifies the execution environment (required).
	// Valid values: "goja", "node", "native".
	Runtime RuntimeType `json:"runtime"`

	// Main is the entry point file path relative to plugin root (optional).
	// Defaults to "index.js" for JS runtimes.
	Main string `json:"main,omitempty"`

	// MinMetiqVersion is the minimum metiq version required (optional).
	// Semantic version constraint, e.g. ">=1.0.0".
	MinMetiqVersion string `json:"min_metiq_version,omitempty"`

	// ─── Distribution ────────────────────────────────────────────────────────

	// DownloadURL is an HTTPS URL for the plugin archive (optional).
	DownloadURL string `json:"download_url,omitempty"`

	// Checksum is a "sha256:<hex>" checksum of the archive (optional).
	Checksum string `json:"checksum,omitempty"`

	// ─── Capabilities ────────────────────────────────────────────────────────

	// Capabilities declares what the plugin provides.
	Capabilities Capabilities `json:"capabilities,omitempty"`

	// ─── Permissions ─────────────────────────────────────────────────────────

	// Permissions declares what the plugin needs access to.
	Permissions Permissions `json:"permissions,omitempty"`

	// ─── Configuration ───────────────────────────────────────────────────────

	// Config describes plugin-specific configuration options.
	Config *ConfigSpec `json:"config,omitempty"`

	// ─── Keywords / Tags ─────────────────────────────────────────────────────

	// Keywords are searchable tags for discovery (optional).
	Keywords []string `json:"keywords,omitempty"`
}

// ─── Supporting Types ────────────────────────────────────────────────────────

// AuthorInfo identifies a plugin author.
type AuthorInfo struct {
	Name   string `json:"name,omitempty"`
	Email  string `json:"email,omitempty"`
	URL    string `json:"url,omitempty"`
	Pubkey string `json:"pubkey,omitempty"` // Nostr pubkey (hex or npub)
}

// RuntimeType identifies the plugin execution environment.
type RuntimeType string

const (
	RuntimeGoja   RuntimeType = "goja"
	RuntimeNode   RuntimeType = "node"
	RuntimeNative RuntimeType = "native"
)

// Valid returns true if the runtime type is recognized.
func (r RuntimeType) Valid() bool {
	switch r {
	case RuntimeGoja, RuntimeNode, RuntimeNative:
		return true
	default:
		return false
	}
}

// ─── Capabilities ────────────────────────────────────────────────────────────

// Capabilities declares what a plugin provides to the metiq runtime.
type Capabilities struct {
	// Tools are agent-callable tools provided by the plugin.
	Tools []ToolCapability `json:"tools,omitempty"`

	// Channels are messaging channel integrations (Telegram, Discord, etc.).
	Channels []ChannelCapability `json:"channels,omitempty"`

	// Hooks are lifecycle hooks the plugin responds to.
	Hooks []HookCapability `json:"hooks,omitempty"`

	// MCPServers are MCP server implementations provided by the plugin.
	MCPServers []MCPServerCapability `json:"mcp_servers,omitempty"`

	// Skills are skill implementations provided by the plugin.
	Skills []SkillCapability `json:"skills,omitempty"`

	// GatewayMethods are additional gateway RPC methods provided by the plugin.
	GatewayMethods []GatewayMethodCapability `json:"gateway_methods,omitempty"`

	// Providers are backend provider implementations (LLM, storage, etc.).
	Providers []ProviderCapability `json:"providers,omitempty"`
}

// IsEmpty returns true if no capabilities are declared.
func (c Capabilities) IsEmpty() bool {
	return len(c.Tools) == 0 &&
		len(c.Channels) == 0 &&
		len(c.Hooks) == 0 &&
		len(c.MCPServers) == 0 &&
		len(c.Skills) == 0 &&
		len(c.GatewayMethods) == 0 &&
		len(c.Providers) == 0
}

// HasSkillExportCapability returns true if the plugin declares skills
// that can be exported for external use.
func (c Capabilities) HasSkillExportCapability() bool {
	for _, skill := range c.Skills {
		if skill.Exportable {
			return true
		}
	}
	return false
}

// ToolCapability describes a tool the plugin provides.
type ToolCapability struct {
	// Name is the tool identifier used in tool calls (required).
	Name string `json:"name"`

	// Description explains what the tool does (required for LLM exposure).
	Description string `json:"description"`

	// Parameters is a JSON Schema describing the tool's input (optional).
	Parameters map[string]any `json:"parameters,omitempty"`

	// Category classifies the tool for permission evaluation (optional).
	// Values: "read", "write", "network", "exec", "dangerous".
	Category ToolCategory `json:"category,omitempty"`

	// RequiresApproval indicates the tool should trigger approval flow (optional).
	RequiresApproval bool `json:"requires_approval,omitempty"`
}

// ToolCategory classifies tools for permission evaluation.
type ToolCategory string

const (
	ToolCategoryRead      ToolCategory = "read"
	ToolCategoryWrite     ToolCategory = "write"
	ToolCategoryNetwork   ToolCategory = "network"
	ToolCategoryExec      ToolCategory = "exec"
	ToolCategoryDangerous ToolCategory = "dangerous"
)

// ChannelCapability describes a channel integration the plugin provides.
type ChannelCapability struct {
	// ID is the channel type identifier (required), e.g. "telegram", "discord".
	ID string `json:"id"`

	// Name is a human-readable name (optional).
	Name string `json:"name,omitempty"`

	// Description explains the channel (optional).
	Description string `json:"description,omitempty"`

	// ConfigSchema is a JSON Schema for channel configuration (optional).
	ConfigSchema map[string]any `json:"config_schema,omitempty"`

	// Features lists supported channel features (optional).
	Features ChannelFeatures `json:"features,omitempty"`
}

// ChannelFeatures describes optional channel capabilities.
type ChannelFeatures struct {
	Typing        bool `json:"typing,omitempty"`
	Reactions     bool `json:"reactions,omitempty"`
	Threads       bool `json:"threads,omitempty"`
	Audio         bool `json:"audio,omitempty"`
	Edit          bool `json:"edit,omitempty"`
	MultiAccount  bool `json:"multi_account,omitempty"`
	E2EEncryption bool `json:"e2e_encryption,omitempty"`
}

// HookCapability describes a lifecycle hook the plugin handles.
type HookCapability struct {
	// Event is the hook event name (required).
	// Values: "message.pre", "message.post", "session.start", "session.end",
	//         "tool.pre", "tool.post", "turn.start", "turn.end".
	Event string `json:"event"`

	// Priority is the execution order (lower = earlier, default 100).
	Priority int `json:"priority,omitempty"`

	// Description explains what the hook does (optional).
	Description string `json:"description,omitempty"`
}

// MCPServerCapability describes an MCP server the plugin provides.
type MCPServerCapability struct {
	// ID is the MCP server identifier (required).
	ID string `json:"id"`

	// Name is a human-readable name (optional).
	Name string `json:"name,omitempty"`

	// Description explains the MCP server (optional).
	Description string `json:"description,omitempty"`

	// Transport is the MCP transport type (required).
	// Values: "stdio", "sse", "http", "websocket", "embedded".
	Transport MCPTransport `json:"transport"`

	// Command is the executable for stdio transport (optional).
	Command string `json:"command,omitempty"`

	// Args are command arguments for stdio transport (optional).
	Args []string `json:"args,omitempty"`

	// URL is the endpoint for HTTP/SSE/WebSocket transports (optional).
	URL string `json:"url,omitempty"`

	// ProvidedTools lists tools this MCP server provides (optional, for docs).
	ProvidedTools []string `json:"provided_tools,omitempty"`

	// ProvidedResources lists resources this MCP server provides (optional).
	ProvidedResources []string `json:"provided_resources,omitempty"`
}

// MCPTransport identifies MCP transport types.
type MCPTransport string

const (
	MCPTransportStdio     MCPTransport = "stdio"
	MCPTransportSSE       MCPTransport = "sse"
	MCPTransportHTTP      MCPTransport = "http"
	MCPTransportWebSocket MCPTransport = "websocket"
	MCPTransportEmbedded  MCPTransport = "embedded"
)

// SkillCapability describes a skill the plugin provides.
type SkillCapability struct {
	// ID is the skill identifier (required).
	ID string `json:"id"`

	// Name is a human-readable name (optional).
	Name string `json:"name,omitempty"`

	// Description explains the skill (optional).
	Description string `json:"description,omitempty"`

	// Instructions are skill-specific system prompt additions (optional).
	Instructions string `json:"instructions,omitempty"`

	// Tools lists tool names this skill requires (optional).
	Tools []string `json:"tools,omitempty"`

	// MCPServers lists MCP server IDs this skill requires (optional).
	MCPServers []string `json:"mcp_servers,omitempty"`

	// Exportable indicates this skill can be exported for external use.
	// When true, the skill may be installed in remote agents or shared.
	// Requires explicit operator opt-in via lifecycle configuration.
	Exportable bool `json:"exportable,omitempty"`

	// ExportRequiresApproval indicates export requires operator approval.
	ExportRequiresApproval bool `json:"export_requires_approval,omitempty"`
}

// GatewayMethodCapability describes a gateway RPC method the plugin provides.
type GatewayMethodCapability struct {
	// Method is the full method name (required), e.g. "myplugin.do_thing".
	Method string `json:"method"`

	// Description explains the method (optional).
	Description string `json:"description,omitempty"`

	// Parameters is a JSON Schema for method parameters (optional).
	Parameters map[string]any `json:"parameters,omitempty"`

	// RequiresAuth indicates the method requires authentication (optional).
	RequiresAuth bool `json:"requires_auth,omitempty"`
}

// ProviderCapability describes a backend provider the plugin implements.
type ProviderCapability struct {
	// Type is the provider type (required).
	// Values: "llm", "embedding", "storage", "secret".
	Type ProviderType `json:"type"`

	// ID is the provider identifier (required).
	ID string `json:"id"`

	// Name is a human-readable name (optional).
	Name string `json:"name,omitempty"`

	// Description explains the provider (optional).
	Description string `json:"description,omitempty"`

	// ConfigSchema is a JSON Schema for provider configuration (optional).
	ConfigSchema map[string]any `json:"config_schema,omitempty"`
}

// ProviderType identifies backend provider types.
type ProviderType string

const (
	ProviderTypeLLM       ProviderType = "llm"
	ProviderTypeEmbedding ProviderType = "embedding"
	ProviderTypeStorage   ProviderType = "storage"
	ProviderTypeSecret    ProviderType = "secret"
)

// ─── Permissions ─────────────────────────────────────────────────────────────

// Permissions declares what the plugin needs access to.
type Permissions struct {
	// Network indicates the plugin needs outbound network access.
	Network *NetworkPermission `json:"network,omitempty"`

	// Filesystem indicates the plugin needs filesystem access.
	Filesystem *FilesystemPermission `json:"filesystem,omitempty"`

	// Exec indicates the plugin needs to spawn processes.
	Exec *ExecPermission `json:"exec,omitempty"`

	// Secrets indicates the plugin needs access to secrets.
	Secrets []string `json:"secrets,omitempty"`

	// Nostr indicates the plugin needs Nostr capabilities.
	Nostr *NostrPermission `json:"nostr,omitempty"`

	// Agent indicates the plugin needs LLM completion access.
	Agent bool `json:"agent,omitempty"`

	// Storage indicates the plugin needs persistent storage.
	Storage bool `json:"storage,omitempty"`
}

// NetworkPermission describes network access requirements.
type NetworkPermission struct {
	// Hosts are allowed host patterns (optional).
	// Supports wildcards: "*.example.com", "api.example.com".
	Hosts []string `json:"hosts,omitempty"`

	// AllowAll permits unrestricted network access (use sparingly).
	AllowAll bool `json:"allow_all,omitempty"`
}

// FilesystemPermission describes filesystem access requirements.
type FilesystemPermission struct {
	// Read lists paths the plugin needs to read (optional).
	Read []string `json:"read,omitempty"`

	// Write lists paths the plugin needs to write (optional).
	Write []string `json:"write,omitempty"`
}

// ExecPermission describes process execution requirements.
type ExecPermission struct {
	// Commands lists allowed commands (optional).
	Commands []string `json:"commands,omitempty"`

	// AllowAll permits unrestricted command execution (dangerous).
	AllowAll bool `json:"allow_all,omitempty"`
}

// NostrPermission describes Nostr capability requirements.
type NostrPermission struct {
	// Publish indicates the plugin needs to publish events.
	Publish bool `json:"publish,omitempty"`

	// Subscribe indicates the plugin needs to subscribe to events.
	Subscribe bool `json:"subscribe,omitempty"`

	// Encrypt indicates the plugin needs NIP-04/44 encryption.
	Encrypt bool `json:"encrypt,omitempty"`

	// Sign indicates the plugin needs to sign with the agent's key.
	Sign bool `json:"sign,omitempty"`
}

// ─── Configuration Spec ──────────────────────────────────────────────────────

// ConfigSpec describes plugin-specific configuration options.
type ConfigSpec struct {
	// Schema is a JSON Schema for plugin configuration.
	Schema map[string]any `json:"schema,omitempty"`

	// Defaults are default configuration values.
	Defaults map[string]any `json:"defaults,omitempty"`
}

// ─── Validation ──────────────────────────────────────────────────────────────

var pluginIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$|^[a-z]$`)
var semverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(-[a-zA-Z0-9.-]+)?(\+[a-zA-Z0-9.-]+)?$`)

// ValidationError represents a manifest validation failure.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ValidationErrors is a collection of validation errors.
type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string {
	if len(e) == 0 {
		return "no errors"
	}
	if len(e) == 1 {
		return e[0].Error()
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d validation errors:\n", len(e)))
	for _, err := range e {
		sb.WriteString("  - ")
		sb.WriteString(err.Error())
		sb.WriteString("\n")
	}
	return sb.String()
}

// Validate checks a manifest for structural and semantic correctness.
// Returns nil if valid, or ValidationErrors with all issues found.
func Validate(m *Manifest) error {
	var errs ValidationErrors

	// Schema version
	if m.SchemaVersion < MinSupportedVersion {
		errs = append(errs, ValidationError{
			Field:   "schema_version",
			Message: fmt.Sprintf("must be >= %d (got %d)", MinSupportedVersion, m.SchemaVersion),
		})
	}

	// ID
	if strings.TrimSpace(m.ID) == "" {
		errs = append(errs, ValidationError{Field: "id", Message: "is required"})
	} else if !pluginIDPattern.MatchString(m.ID) {
		errs = append(errs, ValidationError{
			Field:   "id",
			Message: "must be lowercase alphanumeric with hyphens (e.g. my-plugin)",
		})
	}

	// Version
	if strings.TrimSpace(m.Version) == "" {
		errs = append(errs, ValidationError{Field: "version", Message: "is required"})
	} else if !semverPattern.MatchString(m.Version) {
		errs = append(errs, ValidationError{
			Field:   "version",
			Message: "must be a valid semantic version (e.g. 1.0.0)",
		})
	}

	// Runtime
	if !m.Runtime.Valid() {
		errs = append(errs, ValidationError{
			Field:   "runtime",
			Message: fmt.Sprintf("must be one of: goja, node, native (got %q)", m.Runtime),
		})
	}

	// Validate capabilities
	errs = append(errs, validateCapabilities(&m.Capabilities)...)

	if len(errs) > 0 {
		return errs
	}
	return nil
}

func validateCapabilities(c *Capabilities) ValidationErrors {
	var errs ValidationErrors

	// Validate tools
	seenTools := make(map[string]bool)
	for i, tool := range c.Tools {
		prefix := fmt.Sprintf("capabilities.tools[%d]", i)
		if strings.TrimSpace(tool.Name) == "" {
			errs = append(errs, ValidationError{Field: prefix + ".name", Message: "is required"})
		} else if seenTools[tool.Name] {
			errs = append(errs, ValidationError{Field: prefix + ".name", Message: "is duplicated"})
		} else {
			seenTools[tool.Name] = true
		}
	}

	// Validate channels
	seenChannels := make(map[string]bool)
	for i, ch := range c.Channels {
		prefix := fmt.Sprintf("capabilities.channels[%d]", i)
		if strings.TrimSpace(ch.ID) == "" {
			errs = append(errs, ValidationError{Field: prefix + ".id", Message: "is required"})
		} else if seenChannels[ch.ID] {
			errs = append(errs, ValidationError{Field: prefix + ".id", Message: "is duplicated"})
		} else {
			seenChannels[ch.ID] = true
		}
	}

	// Validate hooks
	for i, hook := range c.Hooks {
		prefix := fmt.Sprintf("capabilities.hooks[%d]", i)
		if strings.TrimSpace(hook.Event) == "" {
			errs = append(errs, ValidationError{Field: prefix + ".event", Message: "is required"})
		}
	}

	// Validate MCP servers
	seenMCP := make(map[string]bool)
	for i, mcp := range c.MCPServers {
		prefix := fmt.Sprintf("capabilities.mcp_servers[%d]", i)
		if strings.TrimSpace(mcp.ID) == "" {
			errs = append(errs, ValidationError{Field: prefix + ".id", Message: "is required"})
		} else if seenMCP[mcp.ID] {
			errs = append(errs, ValidationError{Field: prefix + ".id", Message: "is duplicated"})
		} else {
			seenMCP[mcp.ID] = true
		}
		if mcp.Transport == "" {
			errs = append(errs, ValidationError{Field: prefix + ".transport", Message: "is required"})
		}
	}

	// Validate skills
	seenSkills := make(map[string]bool)
	for i, skill := range c.Skills {
		prefix := fmt.Sprintf("capabilities.skills[%d]", i)
		if strings.TrimSpace(skill.ID) == "" {
			errs = append(errs, ValidationError{Field: prefix + ".id", Message: "is required"})
		} else if seenSkills[skill.ID] {
			errs = append(errs, ValidationError{Field: prefix + ".id", Message: "is duplicated"})
		} else {
			seenSkills[skill.ID] = true
		}
	}

	// Validate gateway methods
	seenMethods := make(map[string]bool)
	for i, method := range c.GatewayMethods {
		prefix := fmt.Sprintf("capabilities.gateway_methods[%d]", i)
		if strings.TrimSpace(method.Method) == "" {
			errs = append(errs, ValidationError{Field: prefix + ".method", Message: "is required"})
		} else if seenMethods[method.Method] {
			errs = append(errs, ValidationError{Field: prefix + ".method", Message: "is duplicated"})
		} else {
			seenMethods[method.Method] = true
		}
	}

	// Validate providers
	for i, prov := range c.Providers {
		prefix := fmt.Sprintf("capabilities.providers[%d]", i)
		if strings.TrimSpace(prov.ID) == "" {
			errs = append(errs, ValidationError{Field: prefix + ".id", Message: "is required"})
		}
		if prov.Type == "" {
			errs = append(errs, ValidationError{Field: prefix + ".type", Message: "is required"})
		}
	}

	return errs
}

// ─── Parsing ─────────────────────────────────────────────────────────────────

// Parse parses and validates a manifest from JSON bytes.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest JSON: %w", err)
	}

	// Set defaults
	if m.SchemaVersion == 0 {
		m.SchemaVersion = 1 // Assume v1 for legacy manifests
	}
	if m.Main == "" && (m.Runtime == RuntimeGoja || m.Runtime == RuntimeNode) {
		m.Main = "index.js"
	}

	if err := Validate(&m); err != nil {
		return nil, err
	}

	return &m, nil
}

// MustParse parses a manifest and panics on error (for testing).
func MustParse(data []byte) *Manifest {
	m, err := Parse(data)
	if err != nil {
		panic(err)
	}
	return m
}

// ─── Conversion ──────────────────────────────────────────────────────────────

// ToJSON serializes a manifest to JSON bytes.
func (m *Manifest) ToJSON() ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

// ─── Compatibility ───────────────────────────────────────────────────────────

// IsCompatible checks if this manifest is compatible with the current runtime.
func (m *Manifest) IsCompatible(metiqVersion string) bool {
	if m.MinMetiqVersion == "" {
		return true
	}
	// TODO: Implement proper semver comparison
	return true
}

// ─── Capability Queries ──────────────────────────────────────────────────────

// HasTools returns true if the manifest declares any tools.
func (m *Manifest) HasTools() bool {
	return len(m.Capabilities.Tools) > 0
}

// HasChannels returns true if the manifest declares any channels.
func (m *Manifest) HasChannels() bool {
	return len(m.Capabilities.Channels) > 0
}

// HasHooks returns true if the manifest declares any hooks.
func (m *Manifest) HasHooks() bool {
	return len(m.Capabilities.Hooks) > 0
}

// HasMCPServers returns true if the manifest declares any MCP servers.
func (m *Manifest) HasMCPServers() bool {
	return len(m.Capabilities.MCPServers) > 0
}

// HasSkills returns true if the manifest declares any skills.
func (m *Manifest) HasSkills() bool {
	return len(m.Capabilities.Skills) > 0
}

// HasGatewayMethods returns true if the manifest declares any gateway methods.
func (m *Manifest) HasGatewayMethods() bool {
	return len(m.Capabilities.GatewayMethods) > 0
}

// HasProviders returns true if the manifest declares any providers.
func (m *Manifest) HasProviders() bool {
	return len(m.Capabilities.Providers) > 0
}

// ToolNames returns a list of all declared tool names.
func (m *Manifest) ToolNames() []string {
	names := make([]string, len(m.Capabilities.Tools))
	for i, t := range m.Capabilities.Tools {
		names[i] = t.Name
	}
	return names
}

// ChannelIDs returns a list of all declared channel IDs.
func (m *Manifest) ChannelIDs() []string {
	ids := make([]string, len(m.Capabilities.Channels))
	for i, c := range m.Capabilities.Channels {
		ids[i] = c.ID
	}
	return ids
}

// MCPServerIDs returns a list of all declared MCP server IDs.
func (m *Manifest) MCPServerIDs() []string {
	ids := make([]string, len(m.Capabilities.MCPServers))
	for i, s := range m.Capabilities.MCPServers {
		ids[i] = s.ID
	}
	return ids
}

// SkillIDs returns a list of all declared skill IDs.
func (m *Manifest) SkillIDs() []string {
	ids := make([]string, len(m.Capabilities.Skills))
	for i, s := range m.Capabilities.Skills {
		ids[i] = s.ID
	}
	return ids
}
