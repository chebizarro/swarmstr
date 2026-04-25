package permissions

import (
	"regexp"
	"strings"
)

// ─── Tool Classification ─────────────────────────────────────────────────────

// RiskLevel indicates the risk associated with a tool or operation.
type RiskLevel string

const (
	// RiskLow indicates minimal risk (read-only operations).
	RiskLow RiskLevel = "low"
	// RiskMedium indicates moderate risk (limited writes, safe network).
	RiskMedium RiskLevel = "medium"
	// RiskHigh indicates significant risk (arbitrary writes, commands).
	RiskHigh RiskLevel = "high"
	// RiskCritical indicates critical risk (system modification, credentials).
	RiskCritical RiskLevel = "critical"
)

// ToolClassification describes a tool's risk profile.
type ToolClassification struct {
	// Category is the tool's general category.
	Category ToolCategory `json:"category"`

	// RiskLevel is the assessed risk.
	RiskLevel RiskLevel `json:"risk_level"`

	// Capabilities lists what the tool can do.
	Capabilities []string `json:"capabilities,omitempty"`

	// RequiresNetwork indicates if network access is needed.
	RequiresNetwork bool `json:"requires_network,omitempty"`

	// RequiresFilesystem indicates if filesystem access is needed.
	RequiresFilesystem bool `json:"requires_filesystem,omitempty"`

	// CanExecuteCode indicates if code execution is possible.
	CanExecuteCode bool `json:"can_execute_code,omitempty"`

	// CanModifySystem indicates if system state can be modified.
	CanModifySystem bool `json:"can_modify_system,omitempty"`

	// Description explains the tool's purpose.
	Description string `json:"description,omitempty"`
}

// ─── Classifier ──────────────────────────────────────────────────────────────

// Classifier automatically categorizes tools based on their names and patterns.
type Classifier struct {
	patterns    []classifierPattern
	overrides   map[string]ToolCategory
	riskMapping map[ToolCategory]RiskLevel
}

// classifierPattern matches tool names to categories.
type classifierPattern struct {
	regex    *regexp.Regexp
	category ToolCategory
}

// NewClassifier creates a new tool classifier with default patterns.
func NewClassifier() *Classifier {
	c := &Classifier{
		overrides: make(map[string]ToolCategory),
		riskMapping: map[ToolCategory]RiskLevel{
			CategoryBuiltin:     RiskMedium,
			CategoryPlugin:      RiskMedium,
			CategoryMCP:         RiskMedium,
			CategoryExec:        RiskHigh,
			CategoryNetwork:     RiskMedium,
			CategoryFilesystem:  RiskMedium,
			CategoryRemoteAgent: RiskHigh,
		},
	}

	// Register default patterns
	c.registerDefaults()

	return c
}

func (c *Classifier) registerDefaults() {
	// MCP tools
	c.addPattern(`^mcp:`, CategoryMCP)
	c.addPattern(`^mcp_`, CategoryMCP)

	// Plugin tools
	c.addPattern(`^plugin:`, CategoryPlugin)
	c.addPattern(`^plugin_`, CategoryPlugin)

	// Execution tools
	c.addPattern(`^bash$`, CategoryExec)
	c.addPattern(`^shell$`, CategoryExec)
	c.addPattern(`^exec$`, CategoryExec)
	c.addPattern(`^run$`, CategoryExec)
	c.addPattern(`^command$`, CategoryExec)
	c.addPattern(`^powershell$`, CategoryExec)
	c.addPattern(`^repl$`, CategoryExec)
	c.addPattern(`^terminal$`, CategoryExec)

	// Filesystem tools
	c.addPattern(`^read`, CategoryFilesystem)
	c.addPattern(`^write`, CategoryFilesystem)
	c.addPattern(`^file`, CategoryFilesystem)
	c.addPattern(`^edit`, CategoryFilesystem)
	c.addPattern(`^create`, CategoryFilesystem)
	c.addPattern(`^delete`, CategoryFilesystem)
	c.addPattern(`^mkdir`, CategoryFilesystem)
	c.addPattern(`^rmdir`, CategoryFilesystem)
	c.addPattern(`^list_dir`, CategoryFilesystem)
	c.addPattern(`^glob`, CategoryFilesystem)
	c.addPattern(`^grep`, CategoryFilesystem)
	c.addPattern(`^search`, CategoryFilesystem)

	// Network tools
	c.addPattern(`^http`, CategoryNetwork)
	c.addPattern(`^fetch`, CategoryNetwork)
	c.addPattern(`^curl`, CategoryNetwork)
	c.addPattern(`^wget`, CategoryNetwork)
	c.addPattern(`^api`, CategoryNetwork)
	c.addPattern(`^web`, CategoryNetwork)
	c.addPattern(`^request`, CategoryNetwork)
	c.addPattern(`^download`, CategoryNetwork)
	c.addPattern(`^upload`, CategoryNetwork)

	// Remote agent tools
	c.addPattern(`^agent`, CategoryRemoteAgent)
	c.addPattern(`^remote`, CategoryRemoteAgent)
	c.addPattern(`^delegate`, CategoryRemoteAgent)
	c.addPattern(`^spawn`, CategoryRemoteAgent)
}

func (c *Classifier) addPattern(pattern string, category ToolCategory) {
	re := regexp.MustCompile(pattern)
	c.patterns = append(c.patterns, classifierPattern{
		regex:    re,
		category: category,
	})
}

// Classify determines the category of a tool.
func (c *Classifier) Classify(toolName string) ToolCategory {
	// Check overrides first
	if cat, ok := c.overrides[toolName]; ok {
		return cat
	}

	// Normalize tool name
	name := strings.ToLower(toolName)

	// Check patterns
	for _, p := range c.patterns {
		if p.regex.MatchString(name) {
			return p.category
		}
	}

	// Default to builtin
	return CategoryBuiltin
}

// SetOverride sets a manual category override for a tool.
func (c *Classifier) SetOverride(toolName string, category ToolCategory) {
	c.overrides[toolName] = category
}

// RemoveOverride removes a manual category override.
func (c *Classifier) RemoveOverride(toolName string) {
	delete(c.overrides, toolName)
}

// GetRiskLevel returns the risk level for a category.
func (c *Classifier) GetRiskLevel(category ToolCategory) RiskLevel {
	if level, ok := c.riskMapping[category]; ok {
		return level
	}
	return RiskMedium
}

// SetRiskLevel sets the risk level for a category.
func (c *Classifier) SetRiskLevel(category ToolCategory, level RiskLevel) {
	c.riskMapping[category] = level
}

// ClassifyFull returns a complete classification for a tool.
func (c *Classifier) ClassifyFull(toolName string) ToolClassification {
	category := c.Classify(toolName)
	riskLevel := c.GetRiskLevel(category)

	classification := ToolClassification{
		Category:  category,
		RiskLevel: riskLevel,
	}

	// Set capabilities based on category
	switch category {
	case CategoryExec:
		classification.CanExecuteCode = true
		classification.CanModifySystem = true
		classification.RequiresFilesystem = true
		classification.Capabilities = []string{"execute_commands", "modify_system", "read_files", "write_files"}

	case CategoryFilesystem:
		classification.RequiresFilesystem = true
		classification.Capabilities = []string{"read_files", "write_files", "list_directories"}

	case CategoryNetwork:
		classification.RequiresNetwork = true
		classification.Capabilities = []string{"http_requests", "fetch_urls"}

	case CategoryMCP:
		classification.RequiresNetwork = true
		classification.Capabilities = []string{"mcp_protocol", "external_service"}

	case CategoryPlugin:
		classification.Capabilities = []string{"plugin_provided"}

	case CategoryRemoteAgent:
		classification.RequiresNetwork = true
		classification.CanExecuteCode = true
		classification.Capabilities = []string{"spawn_agents", "remote_execution"}

	default:
		classification.Capabilities = []string{"builtin"}
	}

	return classification
}

// ─── Content Analysis ────────────────────────────────────────────────────────

// ContentAnalysis provides risk analysis of tool content/arguments.
type ContentAnalysis struct {
	// RiskLevel is the assessed risk of the content.
	RiskLevel RiskLevel `json:"risk_level"`

	// Flags lists concerning patterns found.
	Flags []string `json:"flags,omitempty"`

	// SensitivePaths lists sensitive paths referenced.
	SensitivePaths []string `json:"sensitive_paths,omitempty"`

	// DangerousCommands lists dangerous commands found.
	DangerousCommands []string `json:"dangerous_commands,omitempty"`
}

// dangerousPattern represents a pattern indicating risk.
type dangerousPattern struct {
	regex       *regexp.Regexp
	description string
	level       RiskLevel
}

var dangerousPatterns = []dangerousPattern{
	// Root/system operations
	{regexp.MustCompile(`rm\s+-rf\s+/`), "recursive delete from root", RiskCritical},
	{regexp.MustCompile(`rm\s+-rf\s+\*`), "recursive delete wildcard", RiskHigh},
	{regexp.MustCompile(`dd\s+if=`), "direct disk access", RiskCritical},
	{regexp.MustCompile(`mkfs`), "filesystem creation", RiskCritical},
	{regexp.MustCompile(`fdisk`), "partition modification", RiskCritical},

	// Privilege escalation
	{regexp.MustCompile(`^sudo\s+`), "sudo command", RiskHigh},
	{regexp.MustCompile(`\|\s*sudo`), "piped to sudo", RiskHigh},
	{regexp.MustCompile(`chmod\s+[0-7]*7[0-7]*`), "world-writable permissions", RiskHigh},
	{regexp.MustCompile(`chown.*root`), "change owner to root", RiskHigh},

	// Network exposure
	{regexp.MustCompile(`nc\s+-l`), "netcat listener", RiskHigh},
	{regexp.MustCompile(`0\.0\.0\.0`), "bind to all interfaces", RiskMedium},
	{regexp.MustCompile(`curl.*\|.*sh`), "pipe curl to shell", RiskCritical},
	{regexp.MustCompile(`wget.*\|.*sh`), "pipe wget to shell", RiskCritical},

	// Credentials (case-insensitive patterns)
	{regexp.MustCompile(`(?i)password\s*=`), "password in command", RiskHigh},
	{regexp.MustCompile(`(?i)api[_-]?key\s*=`), "API key in command", RiskHigh},
	{regexp.MustCompile(`(?i)secret\s*=`), "secret in command", RiskHigh},
	{regexp.MustCompile(`(?i)token\s*=`), "token in command", RiskHigh},

	// Code injection
	{regexp.MustCompile(`eval\s*\(`), "eval usage", RiskHigh},
	{regexp.MustCompile(`exec\s*\(`), "exec usage", RiskHigh},
	{regexp.MustCompile(`\$\(.*\)`), "command substitution", RiskMedium},
	{regexp.MustCompile("`.*`"), "backtick execution", RiskMedium},
}

var sensitivePaths = []string{
	"/etc/passwd",
	"/etc/shadow",
	"/etc/sudoers",
	"~/.ssh/",
	"~/.gnupg/",
	"~/.aws/",
	"~/.config/",
	".env",
	".git/config",
	"id_rsa",
	"id_ed25519",
	"credentials",
	"secrets",
}

// riskOrder maps risk levels to numeric values for comparison.
var riskOrder = map[RiskLevel]int{
	RiskLow:      1,
	RiskMedium:   2,
	RiskHigh:     3,
	RiskCritical: 4,
}

// AnalyzeContent examines tool content for security risks.
func AnalyzeContent(content string) ContentAnalysis {
	analysis := ContentAnalysis{
		RiskLevel: RiskLow,
	}

	// Check dangerous patterns
	for _, p := range dangerousPatterns {
		if p.regex.MatchString(content) {
			analysis.Flags = append(analysis.Flags, p.description)
			analysis.DangerousCommands = append(analysis.DangerousCommands, p.regex.FindString(content))
			if riskOrder[p.level] > riskOrder[analysis.RiskLevel] {
				analysis.RiskLevel = p.level
			}
		}
	}

	// Check sensitive paths
	contentLower := strings.ToLower(content)
	for _, path := range sensitivePaths {
		if strings.Contains(contentLower, strings.ToLower(path)) {
			analysis.SensitivePaths = append(analysis.SensitivePaths, path)
			if riskOrder[analysis.RiskLevel] < riskOrder[RiskMedium] {
				analysis.RiskLevel = RiskMedium
			}
		}
	}

	return analysis
}

// ─── Builtin Tool Registry ───────────────────────────────────────────────────

// BuiltinToolInfo describes a known builtin tool.
type BuiltinToolInfo struct {
	Name         string       `json:"name"`
	Category     ToolCategory `json:"category"`
	RiskLevel    RiskLevel    `json:"risk_level"`
	Description  string       `json:"description"`
	Capabilities []string     `json:"capabilities"`
}

// CommonBuiltinTools lists well-known builtin tools and their classifications.
var CommonBuiltinTools = map[string]BuiltinToolInfo{
	"read_file": {
		Name:         "read_file",
		Category:     CategoryFilesystem,
		RiskLevel:    RiskLow,
		Description:  "Read contents of a file",
		Capabilities: []string{"read_files"},
	},
	"write_file": {
		Name:         "write_file",
		Category:     CategoryFilesystem,
		RiskLevel:    RiskMedium,
		Description:  "Write contents to a file",
		Capabilities: []string{"write_files"},
	},
	"edit_file": {
		Name:         "edit_file",
		Category:     CategoryFilesystem,
		RiskLevel:    RiskMedium,
		Description:  "Edit a file with search/replace",
		Capabilities: []string{"read_files", "write_files"},
	},
	"bash": {
		Name:         "bash",
		Category:     CategoryExec,
		RiskLevel:    RiskHigh,
		Description:  "Execute shell commands",
		Capabilities: []string{"execute_commands", "read_files", "write_files", "network", "modify_system"},
	},
	"list_directory": {
		Name:         "list_directory",
		Category:     CategoryFilesystem,
		RiskLevel:    RiskLow,
		Description:  "List directory contents",
		Capabilities: []string{"read_files"},
	},
	"search_files": {
		Name:         "search_files",
		Category:     CategoryFilesystem,
		RiskLevel:    RiskLow,
		Description:  "Search for files by pattern",
		Capabilities: []string{"read_files"},
	},
	"grep": {
		Name:         "grep",
		Category:     CategoryFilesystem,
		RiskLevel:    RiskLow,
		Description:  "Search file contents",
		Capabilities: []string{"read_files"},
	},
	"web_fetch": {
		Name:         "web_fetch",
		Category:     CategoryNetwork,
		RiskLevel:    RiskMedium,
		Description:  "Fetch content from URLs",
		Capabilities: []string{"http_requests"},
	},
	"agent_spawn": {
		Name:         "agent_spawn",
		Category:     CategoryRemoteAgent,
		RiskLevel:    RiskHigh,
		Description:  "Spawn a sub-agent",
		Capabilities: []string{"spawn_agents", "delegate_tasks"},
	},
}

// GetBuiltinToolInfo returns information about a known builtin tool.
func GetBuiltinToolInfo(toolName string) (BuiltinToolInfo, bool) {
	info, ok := CommonBuiltinTools[toolName]
	return info, ok
}
