package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"metiq/internal/memory"
	"metiq/internal/migrate"
)

// ─── memory ───────────────────────────────────────────────────────────────────

func runMemory(args []string) error {
	if len(args) == 0 {
		return runMemoryHelp()
	}
	switch args[0] {
	case "search":
		return runMemorySearchCmd(args[1:])
	case "import-openclaw", "import":
		return runMemoryImportOpenClaw(args[1:])
	case "stats":
		return runMemoryStats(args[1:])
	case "list":
		return runMemoryList(args[1:])
	case "backends":
		return runMemoryBackends(args[1:])
	case "help", "--help", "-h":
		return runMemoryHelp()
	default:
		return fmt.Errorf("unknown memory sub-command %q (search|import-openclaw|stats|list|backends)", args[0])
	}
}

func runMemoryHelp() error {
	fmt.Println(`Usage: metiq memory <command> [options]

Commands:
  search           Search memories (--q <query> [--limit N])
  import-openclaw  Import memories from OpenClaw SQLite database
  stats            Show memory backend statistics
  list             List recent memories
  backends         List available memory backends

Examples:
  metiq memory search --q "project configuration"
  metiq memory import-openclaw ~/.openclaw
  metiq memory import-openclaw --source ~/.openclaw/agents/main/memory/main.sqlite
  metiq memory stats`)
	return nil
}

// ─── memory import-openclaw ───────────────────────────────────────────────────

func runMemoryImportOpenClaw(args []string) error {
	fs := flag.NewFlagSet("memory import-openclaw", flag.ContinueOnError)

	var (
		sourcePath string
		targetPath string
		backend    string
		dryRun     bool
		dedupe     bool
		verbose    bool
		jsonOut    bool
	)

	fs.StringVar(&sourcePath, "source", "", "OpenClaw home dir or SQLite database path")
	fs.StringVar(&targetPath, "target", "", "Target database path (default: auto based on backend)")
	fs.StringVar(&backend, "backend", "sqlite", "Target backend: sqlite, json-fts, qdrant")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview import without writing")
	fs.BoolVar(&dedupe, "dedupe", true, "Skip duplicate entries by content hash")
	fs.BoolVar(&verbose, "verbose", false, "Verbose output")
	fs.BoolVar(&jsonOut, "json", false, "Output results as JSON")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: metiq memory import-openclaw [options] [source-path]

Import memories from an OpenClaw installation into Metiq.

Source can be:
  - OpenClaw home directory (e.g., ~/.openclaw)
  - Specific SQLite database file (e.g., ~/.openclaw/agents/main/memory/main.sqlite)

Options:
`)
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Examples:
  # Import from OpenClaw home (scans all agent memory databases)
  metiq memory import-openclaw ~/.openclaw

  # Import specific database
  metiq memory import-openclaw --source ~/.openclaw/agents/main/memory/main.sqlite

  # Dry run to preview
  metiq memory import-openclaw --dry-run ~/.openclaw

  # Import to JSON-FTS backend instead of SQLite
  metiq memory import-openclaw --backend json-fts ~/.openclaw
`)
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Handle positional argument
	if fs.NArg() > 0 && sourcePath == "" {
		sourcePath = fs.Arg(0)
	}

	if sourcePath == "" {
		// Default to ~/.openclaw
		home, _ := os.UserHomeDir()
		sourcePath = filepath.Join(home, ".openclaw")
	}

	// Expand ~ in paths
	sourcePath = expandPath(sourcePath)
	if targetPath != "" {
		targetPath = expandPath(targetPath)
	}

	// Determine source paths based on input
	var sourcePaths []string
	info, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("source not found: %s", sourcePath)
	}

	if info.IsDir() {
		// It's a directory - scan for SQLite databases
		pattern := filepath.Join(sourcePath, "agents", "*", "memory", "*.sqlite")
		matches, _ := filepath.Glob(pattern)
		if len(matches) == 0 {
			// Try direct path if it looks like an agents directory
			pattern = filepath.Join(sourcePath, "*", "memory", "*.sqlite")
			matches, _ = filepath.Glob(pattern)
		}
		if len(matches) == 0 {
			return fmt.Errorf("no OpenClaw memory databases found in %s", sourcePath)
		}
		sourcePaths = matches
	} else {
		// It's a file - use directly
		sourcePaths = []string{sourcePath}
	}

	if verbose {
		fmt.Printf("Found %d memory database(s):\n", len(sourcePaths))
		for _, p := range sourcePaths {
			fmt.Printf("  - %s\n", p)
		}
		fmt.Println()
	}

	// Set default target path based on backend
	if targetPath == "" {
		home, _ := os.UserHomeDir()
		switch backend {
		case "sqlite":
			targetPath = filepath.Join(home, ".metiq", "memory.sqlite")
		case "json-fts", "memory":
			targetPath = filepath.Join(home, ".metiq", "memory-index.json")
		case "qdrant":
			targetPath = "qdrant://localhost:6333/metiq-memory"
		default:
			return fmt.Errorf("unknown backend: %s (sqlite, json-fts, qdrant)", backend)
		}
	}

	// Configure and run import
	cfg := migrate.MemoryImportConfig{
		SourcePaths:    sourcePaths,
		TargetPath:     targetPath,
		Deduplicate:    dedupe,
		CopyEmbeddings: true,
		DryRun:         dryRun,
		Verbose:        verbose,
	}

	if verbose || dryRun {
		fmt.Printf("Importing to: %s (backend: %s)\n", targetPath, backend)
		if dryRun {
			fmt.Println("DRY RUN - no changes will be made\n")
		}
	}

	importer := migrate.NewMemoryImporter(cfg)
	stats, err := importer.Import()
	if err != nil {
		return fmt.Errorf("import failed: %w", err)
	}

	// Output results
	if jsonOut {
		data, _ := json.MarshalIndent(stats, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	// Pretty print results
	fmt.Printf("✅ Memory import complete\n\n")
	fmt.Printf("Databases scanned:  %d\n", stats.DatabasesFound)
	fmt.Printf("Databases imported: %d\n", stats.DatabasesImported)
	fmt.Printf("Chunks found:       %d\n", stats.ChunksFound)
	fmt.Printf("Chunks imported:    %d\n", stats.ChunksImported)
	if stats.ChunksDeduplicated > 0 {
		fmt.Printf("Chunks deduplicated: %d\n", stats.ChunksDeduplicated)
	}
	if stats.ChunksSkipped > 0 {
		fmt.Printf("Chunks skipped:     %d\n", stats.ChunksSkipped)
	}
	if stats.EmbeddingsCopied > 0 {
		fmt.Printf("Embeddings copied:  %d\n", stats.EmbeddingsCopied)
	}
	fmt.Printf("Duration:           %dms\n", stats.DurationMs)

	if len(stats.Errors) > 0 {
		fmt.Printf("\n⚠️  %d errors occurred:\n", len(stats.Errors))
		for _, e := range stats.Errors {
			fmt.Printf("  - %s\n", e)
		}
	}

	return nil
}

// ─── memory search ────────────────────────────────────────────────────────────

func runMemorySearchCmd(args []string) error {
	fs := flag.NewFlagSet("memory search", flag.ContinueOnError)
	var query string
	var limit int
	var backend string
	var jsonOut bool

	fs.StringVar(&query, "q", "", "search query")
	fs.IntVar(&limit, "limit", 10, "max results")
	fs.StringVar(&backend, "backend", "sqlite", "memory backend (sqlite, json-fts)")
	fs.BoolVar(&jsonOut, "json", false, "output as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("memory search requires --q <query>")
	}

	// Open backend
	home, _ := os.UserHomeDir()
	var backendPath string
	switch backend {
	case "sqlite":
		backendPath = filepath.Join(home, ".metiq", "memory.sqlite")
	case "json-fts", "memory":
		backendPath = filepath.Join(home, ".metiq", "memory-index.json")
	default:
		backendPath = ""
	}

	store, err := memory.OpenBackend(backend, backendPath)
	if err != nil {
		return fmt.Errorf("open backend: %w", err)
	}
	defer store.Close()

	results := store.Search(query, limit)

	if jsonOut {
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(results) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	fmt.Printf("Found %d results:\n\n", len(results))
	for i, r := range results {
		fmt.Printf("%d. [%s] %s\n", i+1, r.Topic, truncate(r.Text, 100))
		if len(r.Keywords) > 0 {
			fmt.Printf("   Keywords: %s\n", strings.Join(r.Keywords, ", "))
		}
		fmt.Println()
	}

	return nil
}

// ─── memory stats ─────────────────────────────────────────────────────────────

func runMemoryStats(args []string) error {
	fs := flag.NewFlagSet("memory stats", flag.ContinueOnError)
	var backend string
	var jsonOut bool

	fs.StringVar(&backend, "backend", "sqlite", "memory backend (sqlite, json-fts)")
	fs.BoolVar(&jsonOut, "json", false, "output as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	home, _ := os.UserHomeDir()
	var backendPath string
	switch backend {
	case "sqlite":
		backendPath = filepath.Join(home, ".metiq", "memory.sqlite")
	default:
		backendPath = filepath.Join(home, ".metiq", "memory-index.json")
	}

	store, err := memory.OpenBackend(backend, backendPath)
	if err != nil {
		return fmt.Errorf("open backend: %w", err)
	}
	defer store.Close()

	stats := map[string]any{
		"backend":        backend,
		"path":           backendPath,
		"total_memories": store.Count(),
		"total_sessions": store.SessionCount(),
	}

	// Get file size
	if info, err := os.Stat(backendPath); err == nil {
		stats["file_size_bytes"] = info.Size()
	}

	if jsonOut {
		data, _ := json.MarshalIndent(stats, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	fmt.Println("Memory Backend Statistics")
	fmt.Println("─────────────────────────")
	fmt.Printf("Backend:         %s\n", backend)
	fmt.Printf("Path:            %s\n", backendPath)
	fmt.Printf("Total memories:  %d\n", stats["total_memories"])
	fmt.Printf("Total sessions:  %d\n", stats["total_sessions"])
	if size, ok := stats["file_size_bytes"].(int64); ok {
		fmt.Printf("Database size:   %s\n", formatBytes(size))
	}

	return nil
}

// ─── memory list ──────────────────────────────────────────────────────────────

func runMemoryList(args []string) error {
	fs := flag.NewFlagSet("memory list", flag.ContinueOnError)
	var limit int
	var topic string
	var memType string
	var backend string
	var jsonOut bool

	fs.IntVar(&limit, "limit", 20, "max results")
	fs.StringVar(&topic, "topic", "", "filter by topic")
	fs.StringVar(&memType, "type", "", "filter by type")
	fs.StringVar(&backend, "backend", "sqlite", "memory backend")
	fs.BoolVar(&jsonOut, "json", false, "output as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	home, _ := os.UserHomeDir()
	var backendPath string
	switch backend {
	case "sqlite":
		backendPath = filepath.Join(home, ".metiq", "memory.sqlite")
	default:
		backendPath = filepath.Join(home, ".metiq", "memory-index.json")
	}

	store, err := memory.OpenBackend(backend, backendPath)
	if err != nil {
		return fmt.Errorf("open backend: %w", err)
	}
	defer store.Close()

	var results []memory.IndexedMemory
	if topic != "" {
		results = store.ListByTopic(topic, limit)
	} else if memType != "" {
		results = store.ListByType(memType, limit)
	} else {
		// List recent by searching with empty query equivalent
		results = store.Search("*", limit)
	}

	if jsonOut {
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(results) == 0 {
		fmt.Println("No memories found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTOPIC\tTYPE\tTEXT")
	fmt.Fprintln(w, "──\t─────\t────\t────")
	for _, r := range results {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			truncate(r.MemoryID, 12),
			truncate(r.Topic, 20),
			truncate(r.Type, 15),
			truncate(r.Text, 50))
	}
	w.Flush()

	return nil
}

// ─── memory backends ──────────────────────────────────────────────────────────

func runMemoryBackends(args []string) error {
	backends := memory.ListBackends()
	fmt.Println("Available memory backends:")
	for _, b := range backends {
		fmt.Printf("  - %s\n", b)
	}
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
