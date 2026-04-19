// Package mcp/defaults provides auto-detected MCP server configurations for
// coding-relevant services. Servers are enabled based on environment variables
// and tool availability. User config takes precedence over defaults.
package mcp

import (
	"os"
	"os/exec"
	"strings"
)

// ConfigSourceDefaults is the source identifier for built-in default servers.
const ConfigSourceDefaults ConfigSource = "defaults.coding"

// defaultsPrecedence is lower than user config (extraMCPPrecedence = 100)
// so user definitions always win over auto-detected defaults.
const defaultsPrecedence = 50

// DefaultServerOpts configures default MCP server discovery.
type DefaultServerOpts struct {
	// WorkspaceDir is the agent's workspace directory, used for filesystem
	// server scoping.
	WorkspaceDir string

	// CheckCommand overrides the default command availability check.
	// If nil, exec.LookPath is used. Provided for testing.
	CheckCommand func(string) bool
}

func (o DefaultServerOpts) commandAvailable(name string) bool {
	if o.CheckCommand != nil {
		return o.CheckCommand(name)
	}
	_, err := exec.LookPath(name)
	return err == nil
}

// DefaultCodingServers returns auto-detected MCP server configs for coding
// tasks. Servers are discovered from environment variables:
//
//   - github: GITHUB_PERSONAL_ACCESS_TOKEN or GITHUB_TOKEN
//   - postgres: DATABASE_URL, POSTGRES_URL, or POSTGRESQL_URL
//   - sqlite: SQLITE_DB (path to database file)
//   - filesystem: METIQ_MCP_FILESYSTEM (path or "true" for workspace)
//
// All stdio-based servers require npx to be available. User config
// (precedence 100) overrides these defaults (precedence 50).
func DefaultCodingServers(opts DefaultServerOpts) SourceConfig {
	source := SourceConfig{
		Source:     ConfigSourceDefaults,
		Enabled:    true,
		Precedence: defaultsPrecedence,
		Servers:    make(map[string]ServerConfig),
	}

	if !opts.commandAvailable("npx") {
		source.Enabled = false
		return source
	}

	addGitHubServer(&source, opts)
	addPostgresServer(&source, opts)
	addSQLiteServer(&source, opts)
	addFilesystemServer(&source, opts)

	if len(source.Servers) == 0 {
		source.Enabled = false
	}

	return source
}

// addGitHubServer registers the GitHub MCP server when a personal access token
// or standard GitHub token is available.
func addGitHubServer(source *SourceConfig, _ DefaultServerOpts) {
	token := firstEnvValue("GITHUB_PERSONAL_ACCESS_TOKEN", "GITHUB_TOKEN")
	if token == "" {
		return
	}
	source.Servers["github"] = ServerConfig{
		Enabled: true,
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-github"},
		Env:     map[string]string{"GITHUB_PERSONAL_ACCESS_TOKEN": token},
	}
}

// addPostgresServer registers the PostgreSQL MCP server when a connection URL
// is available. Provides schema inspection and query execution.
func addPostgresServer(source *SourceConfig, _ DefaultServerOpts) {
	url := firstEnvValue("DATABASE_URL", "POSTGRES_URL", "POSTGRESQL_URL")
	if url == "" {
		return
	}
	source.Servers["postgres"] = ServerConfig{
		Enabled: true,
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-postgres", url},
	}
}

// addSQLiteServer registers the SQLite MCP server when a database path is
// configured. Provides schema inspection and query execution.
func addSQLiteServer(source *SourceConfig, _ DefaultServerOpts) {
	db := strings.TrimSpace(os.Getenv("SQLITE_DB"))
	if db == "" {
		return
	}
	source.Servers["sqlite"] = ServerConfig{
		Enabled: true,
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-sqlite", "--db-path", db},
	}
}

// addFilesystemServer registers the filesystem MCP server. This is opt-in via
// METIQ_MCP_FILESYSTEM since the agent already has superior built-in filesystem
// tools. Set to a path to scope the server, or "true"/"1" to use the workspace.
func addFilesystemServer(source *SourceConfig, opts DefaultServerOpts) {
	dir := strings.TrimSpace(os.Getenv("METIQ_MCP_FILESYSTEM"))
	if dir == "" {
		return
	}
	if dir == "1" || dir == "true" {
		dir = opts.WorkspaceDir
	}
	if dir == "" {
		return
	}
	source.Servers["filesystem"] = ServerConfig{
		Enabled: true,
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", dir},
	}
}

// firstEnvValue returns the value of the first non-empty environment variable.
func firstEnvValue(keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}
