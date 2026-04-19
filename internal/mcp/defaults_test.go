package mcp

import (
	"testing"
)

func alwaysAvailable(_ string) bool { return true }
func neverAvailable(_ string) bool  { return false }

// ─── DefaultCodingServers ───────────────────────────────────────────────────

func TestDefaultCodingServers_NoNpx(t *testing.T) {
	source := DefaultCodingServers(DefaultServerOpts{
		CheckCommand: neverAvailable,
	})
	if source.Enabled {
		t.Error("should be disabled when npx is unavailable")
	}
	if len(source.Servers) != 0 {
		t.Errorf("should have no servers, got %d", len(source.Servers))
	}
}

func TestDefaultCodingServers_NoEnvVars(t *testing.T) {
	// Clear all relevant env vars.
	for _, key := range []string{
		"GITHUB_PERSONAL_ACCESS_TOKEN", "GITHUB_TOKEN",
		"DATABASE_URL", "POSTGRES_URL", "POSTGRESQL_URL",
		"SQLITE_DB",
		"METIQ_MCP_FILESYSTEM",
	} {
		t.Setenv(key, "")
	}

	source := DefaultCodingServers(DefaultServerOpts{
		CheckCommand: alwaysAvailable,
	})
	if source.Enabled {
		t.Error("should be disabled when no env vars set")
	}
	if len(source.Servers) != 0 {
		t.Errorf("should have no servers, got %d", len(source.Servers))
	}
}

func TestDefaultCodingServers_SourceMetadata(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_test")
	source := DefaultCodingServers(DefaultServerOpts{
		CheckCommand: alwaysAvailable,
	})
	if source.Source != ConfigSourceDefaults {
		t.Errorf("source = %q, want %q", source.Source, ConfigSourceDefaults)
	}
	if source.Precedence != defaultsPrecedence {
		t.Errorf("precedence = %d, want %d", source.Precedence, defaultsPrecedence)
	}
}

// ─── GitHub ─────────────────────────────────────────────────────────────────

func TestDefaultCodingServers_GitHub_PAT(t *testing.T) {
	t.Setenv("GITHUB_PERSONAL_ACCESS_TOKEN", "ghp_pat123")
	t.Setenv("GITHUB_TOKEN", "")

	source := DefaultCodingServers(DefaultServerOpts{
		CheckCommand: alwaysAvailable,
	})
	gh, ok := source.Servers["github"]
	if !ok {
		t.Fatal("expected github server")
	}
	if !gh.Enabled {
		t.Error("github should be enabled")
	}
	if gh.Command != "npx" {
		t.Errorf("command = %q, want npx", gh.Command)
	}
	if len(gh.Args) < 2 || gh.Args[1] != "@modelcontextprotocol/server-github" {
		t.Errorf("unexpected args: %v", gh.Args)
	}
	if gh.Env["GITHUB_PERSONAL_ACCESS_TOKEN"] != "ghp_pat123" {
		t.Error("env should contain the token")
	}
}

func TestDefaultCodingServers_GitHub_Token(t *testing.T) {
	t.Setenv("GITHUB_PERSONAL_ACCESS_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "gho_token456")

	source := DefaultCodingServers(DefaultServerOpts{
		CheckCommand: alwaysAvailable,
	})
	gh, ok := source.Servers["github"]
	if !ok {
		t.Fatal("expected github server from GITHUB_TOKEN")
	}
	if gh.Env["GITHUB_PERSONAL_ACCESS_TOKEN"] != "gho_token456" {
		t.Error("should pass GITHUB_TOKEN as GITHUB_PERSONAL_ACCESS_TOKEN env")
	}
}

func TestDefaultCodingServers_GitHub_PATTakesPrecedence(t *testing.T) {
	t.Setenv("GITHUB_PERSONAL_ACCESS_TOKEN", "pat_first")
	t.Setenv("GITHUB_TOKEN", "token_second")

	source := DefaultCodingServers(DefaultServerOpts{
		CheckCommand: alwaysAvailable,
	})
	gh := source.Servers["github"]
	if gh.Env["GITHUB_PERSONAL_ACCESS_TOKEN"] != "pat_first" {
		t.Error("PAT should take precedence over GITHUB_TOKEN")
	}
}

// ─── PostgreSQL ─────────────────────────────────────────────────────────────

func TestDefaultCodingServers_Postgres_DatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://localhost:5432/mydb")
	t.Setenv("POSTGRES_URL", "")
	t.Setenv("POSTGRESQL_URL", "")

	source := DefaultCodingServers(DefaultServerOpts{
		CheckCommand: alwaysAvailable,
	})
	pg, ok := source.Servers["postgres"]
	if !ok {
		t.Fatal("expected postgres server")
	}
	if !pg.Enabled {
		t.Error("postgres should be enabled")
	}
	if pg.Command != "npx" {
		t.Errorf("command = %q", pg.Command)
	}
	// The connection URL should be in args.
	found := false
	for _, arg := range pg.Args {
		if arg == "postgresql://localhost:5432/mydb" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("args should contain connection URL: %v", pg.Args)
	}
}

func TestDefaultCodingServers_Postgres_FallbackEnvVars(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("POSTGRES_URL", "")
	t.Setenv("POSTGRESQL_URL", "postgresql://fallback:5432/db")

	source := DefaultCodingServers(DefaultServerOpts{
		CheckCommand: alwaysAvailable,
	})
	_, ok := source.Servers["postgres"]
	if !ok {
		t.Fatal("expected postgres from POSTGRESQL_URL fallback")
	}
}

// ─── SQLite ─────────────────────────────────────────────────────────────────

func TestDefaultCodingServers_SQLite(t *testing.T) {
	t.Setenv("SQLITE_DB", "/data/app.db")

	source := DefaultCodingServers(DefaultServerOpts{
		CheckCommand: alwaysAvailable,
	})
	sq, ok := source.Servers["sqlite"]
	if !ok {
		t.Fatal("expected sqlite server")
	}
	if !sq.Enabled {
		t.Error("sqlite should be enabled")
	}
	// Should contain --db-path and the path.
	foundPath := false
	for i, arg := range sq.Args {
		if arg == "--db-path" && i+1 < len(sq.Args) && sq.Args[i+1] == "/data/app.db" {
			foundPath = true
			break
		}
	}
	if !foundPath {
		t.Errorf("args should contain --db-path /data/app.db: %v", sq.Args)
	}
}

func TestDefaultCodingServers_SQLite_NotSetWhenEmpty(t *testing.T) {
	t.Setenv("SQLITE_DB", "")

	source := DefaultCodingServers(DefaultServerOpts{
		CheckCommand: alwaysAvailable,
	})
	if _, ok := source.Servers["sqlite"]; ok {
		t.Error("sqlite should not be set when SQLITE_DB is empty")
	}
}

// ─── Filesystem ─────────────────────────────────────────────────────────────

func TestDefaultCodingServers_Filesystem_ExplicitPath(t *testing.T) {
	t.Setenv("METIQ_MCP_FILESYSTEM", "/projects/myapp")

	source := DefaultCodingServers(DefaultServerOpts{
		CheckCommand: alwaysAvailable,
	})
	fs, ok := source.Servers["filesystem"]
	if !ok {
		t.Fatal("expected filesystem server")
	}
	found := false
	for _, arg := range fs.Args {
		if arg == "/projects/myapp" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("args should contain the path: %v", fs.Args)
	}
}

func TestDefaultCodingServers_Filesystem_TrueUsesWorkspace(t *testing.T) {
	t.Setenv("METIQ_MCP_FILESYSTEM", "true")

	source := DefaultCodingServers(DefaultServerOpts{
		CheckCommand: alwaysAvailable,
		WorkspaceDir: "/home/user/workspace",
	})
	fs, ok := source.Servers["filesystem"]
	if !ok {
		t.Fatal("expected filesystem server")
	}
	found := false
	for _, arg := range fs.Args {
		if arg == "/home/user/workspace" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("should use workspace dir: %v", fs.Args)
	}
}

func TestDefaultCodingServers_Filesystem_OneUsesWorkspace(t *testing.T) {
	t.Setenv("METIQ_MCP_FILESYSTEM", "1")

	source := DefaultCodingServers(DefaultServerOpts{
		CheckCommand: alwaysAvailable,
		WorkspaceDir: "/workspace",
	})
	if _, ok := source.Servers["filesystem"]; !ok {
		t.Fatal("expected filesystem server with '1'")
	}
}

func TestDefaultCodingServers_Filesystem_TrueNoWorkspace(t *testing.T) {
	t.Setenv("METIQ_MCP_FILESYSTEM", "true")

	source := DefaultCodingServers(DefaultServerOpts{
		CheckCommand: alwaysAvailable,
		WorkspaceDir: "",
	})
	if _, ok := source.Servers["filesystem"]; ok {
		t.Error("filesystem should not be set when workspace is empty")
	}
}

func TestDefaultCodingServers_Filesystem_NotSetWhenEmpty(t *testing.T) {
	t.Setenv("METIQ_MCP_FILESYSTEM", "")

	source := DefaultCodingServers(DefaultServerOpts{
		CheckCommand: alwaysAvailable,
	})
	if _, ok := source.Servers["filesystem"]; ok {
		t.Error("filesystem should not be set when env var is empty")
	}
}

// ─── Multiple servers ───────────────────────────────────────────────────────

func TestDefaultCodingServers_MultipleEnabled(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_test")
	t.Setenv("DATABASE_URL", "postgresql://localhost/db")
	t.Setenv("SQLITE_DB", "/tmp/test.db")
	t.Setenv("METIQ_MCP_FILESYSTEM", "/src")

	source := DefaultCodingServers(DefaultServerOpts{
		CheckCommand: alwaysAvailable,
	})
	if !source.Enabled {
		t.Error("should be enabled")
	}
	if len(source.Servers) != 4 {
		t.Errorf("expected 4 servers, got %d", len(source.Servers))
	}
	for _, name := range []string{"github", "postgres", "sqlite", "filesystem"} {
		if _, ok := source.Servers[name]; !ok {
			t.Errorf("missing server: %s", name)
		}
	}
}

// ─── firstEnvValue ──────────────────────────────────────────────────────────

func TestFirstEnvValue_FirstWins(t *testing.T) {
	t.Setenv("TEST_A", "alpha")
	t.Setenv("TEST_B", "beta")
	result := firstEnvValue("TEST_A", "TEST_B")
	if result != "alpha" {
		t.Errorf("got %q, want alpha", result)
	}
}

func TestFirstEnvValue_SkipsEmpty(t *testing.T) {
	t.Setenv("TEST_A", "")
	t.Setenv("TEST_B", "beta")
	result := firstEnvValue("TEST_A", "TEST_B")
	if result != "beta" {
		t.Errorf("got %q, want beta", result)
	}
}

func TestFirstEnvValue_AllEmpty(t *testing.T) {
	t.Setenv("TEST_A", "")
	t.Setenv("TEST_B", "")
	result := firstEnvValue("TEST_A", "TEST_B")
	if result != "" {
		t.Errorf("got %q, want empty", result)
	}
}

func TestFirstEnvValue_WhitespaceOnly(t *testing.T) {
	t.Setenv("TEST_A", "  ")
	t.Setenv("TEST_B", "value")
	result := firstEnvValue("TEST_A", "TEST_B")
	if result != "value" {
		t.Errorf("got %q, want value (whitespace-only should be skipped)", result)
	}
}

// ─── ResolveConfigDocWithDefaults ───────────────────────────────────────────

func TestResolveConfigDocWithDefaults_MergesDefaults(t *testing.T) {
	defaults := SourceConfig{
		Source:     ConfigSourceDefaults,
		Enabled:    true,
		Precedence: defaultsPrecedence,
		Servers: map[string]ServerConfig{
			"github": {
				Enabled: true,
				Command: "npx",
				Args:    []string{"-y", "@modelcontextprotocol/server-github"},
			},
		},
	}

	// No user MCP config.
	cfg := ResolveSourceConfigs(defaults)
	if _, ok := cfg.Servers["github"]; !ok {
		t.Fatal("expected github from defaults")
	}
}

func TestResolveConfigDocWithDefaults_UserOverridesDefaults(t *testing.T) {
	userSource := SourceConfig{
		Source:     ConfigSourceExtraMCP,
		Enabled:    true,
		Precedence: extraMCPPrecedence, // 100 > 50
		Servers: map[string]ServerConfig{
			"github": {
				Enabled: true,
				Command: "custom-github-server",
			},
		},
	}
	defaults := SourceConfig{
		Source:     ConfigSourceDefaults,
		Enabled:    true,
		Precedence: defaultsPrecedence, // 50
		Servers: map[string]ServerConfig{
			"github": {
				Enabled: true,
				Command: "npx",
				Args:    []string{"-y", "@modelcontextprotocol/server-github"},
			},
		},
	}

	cfg := ResolveSourceConfigs(userSource, defaults)
	gh, ok := cfg.Servers["github"]
	if !ok {
		t.Fatal("expected github server")
	}
	if gh.Command != "custom-github-server" {
		t.Errorf("user config should win: command = %q", gh.Command)
	}
}

func TestResolveConfigDocWithDefaults_DefaultsAddNewServers(t *testing.T) {
	userSource := SourceConfig{
		Source:     ConfigSourceExtraMCP,
		Enabled:    true,
		Precedence: extraMCPPrecedence,
		Servers: map[string]ServerConfig{
			"custom_server": {
				Enabled: true,
				Command: "my-server",
			},
		},
	}
	defaults := SourceConfig{
		Source:     ConfigSourceDefaults,
		Enabled:    true,
		Precedence: defaultsPrecedence,
		Servers: map[string]ServerConfig{
			"github": {
				Enabled: true,
				Command: "npx",
				Args:    []string{"-y", "@modelcontextprotocol/server-github"},
			},
		},
	}

	cfg := ResolveSourceConfigs(userSource, defaults)
	if _, ok := cfg.Servers["custom_server"]; !ok {
		t.Error("user server should still be present")
	}
	if _, ok := cfg.Servers["github"]; !ok {
		t.Error("default server should be added alongside user servers")
	}
}
