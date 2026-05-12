package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"metiq/internal/config"
	"metiq/internal/memory"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/plugins/registry"
	"metiq/internal/policy"
)

// version is set at build time via -ldflags "-X main.version=<tag>".
var version = "0.0.0-dev"

func main() {
	var bootstrapPath string
	flag.StringVar(&bootstrapPath, "bootstrap", "", "path to bootstrap config JSON")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		return
	}

	registry := newCommandRegistry(bootstrapPath)
	handled, err := registry.dispatch(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", args[0], err)
		os.Exit(1)
	}
	if !handled {
		usage()
		os.Exit(2)
	}
}

func runDMSend(bootstrapPath string, args []string) error {
	fs := flag.NewFlagSet("dm-send", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var to string
	var text string
	var timeoutSec int
	fs.StringVar(&to, "to", "", "recipient npub/hex pubkey")
	fs.StringVar(&text, "text", "", "plaintext message")
	fs.IntVar(&timeoutSec, "timeout", 15, "publish timeout seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if to == "" || text == "" {
		return fmt.Errorf("dm-send requires --to and --text")
	}

	cfg, err := config.LoadBootstrap(bootstrapPath)
	if err != nil {
		return err
	}
	privateKey, err := config.ResolvePrivateKey(cfg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	eventID, err := nostruntime.SendDMOnce(ctx, privateKey, cfg.Relays, to, text)
	if err != nil {
		return err
	}
	fmt.Printf("dm published event_id=%s\n", eventID)
	return nil
}

func runMemorySearch(args []string) error {
	fs := flag.NewFlagSet("memory-search", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var query string
	var limit int
	fs.StringVar(&query, "q", "", "search query")
	fs.IntVar(&limit, "limit", 10, "max results")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("memory-search requires --q")
	}

	index, err := memory.OpenIndex("")
	if err != nil {
		return err
	}
	results := index.Search(query, limit)
	for _, r := range results {
		fmt.Printf("[%s] session=%s topic=%s text=%q\n", r.MemoryID, r.SessionID, r.Topic, r.Text)
	}
	if len(results) == 0 {
		fmt.Println("no results")
	}
	return nil
}

func runConfigExport(args []string) error {
	fs := flag.NewFlagSet("config-export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var path string
	var outFile string
	var doRedact bool
	fs.StringVar(&path, "path", "", "config file path (default: ~/.metiq/config.json)")
	fs.StringVar(&outFile, "out", "", "output file path (default: stdout)")
	fs.BoolVar(&doRedact, "redact", false, "redact sensitive fields before printing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if path == "" {
		def, err := config.DefaultConfigPath()
		if err != nil {
			return fmt.Errorf("resolve default config path: %w", err)
		}
		path = def
	}
	doc, err := config.LoadConfigFile(path)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if doRedact {
		doc = config.Redact(doc)
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	out = append(out, '\n')
	if outFile == "" {
		_, err = os.Stdout.Write(out)
		return err
	}
	return os.WriteFile(outFile, out, 0o600)
}

func runConfigImport(args []string) error {
	fs := flag.NewFlagSet("config-import", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var path string
	var srcFile string
	var dryRun bool
	fs.StringVar(&path, "path", "", "target config file path (default: ~/.metiq/config.json)")
	fs.StringVar(&srcFile, "file", "", "source config file (default: stdin)")
	fs.BoolVar(&dryRun, "dry-run", false, "validate only, do not write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if path == "" {
		def, err := config.DefaultConfigPath()
		if err != nil {
			return fmt.Errorf("resolve default config path: %w", err)
		}
		path = def
	}
	var err error
	path, err = config.ValidateConfigWritePath(path)
	if err != nil {
		return err
	}
	// Read source.
	var raw []byte
	if srcFile == "" {
		var err error
		raw, err = io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
	} else {
		var err error
		srcFile, err = config.ValidateConfigFilePath(srcFile)
		if err != nil {
			return err
		}
		raw, err = os.ReadFile(srcFile)
		if err != nil {
			return fmt.Errorf("read source file: %w", err)
		}
	}
	// Validate: must parse as ConfigDoc.
	doc, err := config.ParseConfigBytes(raw, srcFile)
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	if errs := config.ValidateConfigDoc(doc); len(errs) > 0 {
		return fmt.Errorf("validate config: %v", errs[0])
	}
	if err := policy.ValidateConfig(policy.NormalizeConfig(doc)); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	if dryRun {
		out, _ := json.MarshalIndent(doc, "", "  ")
		fmt.Printf("config valid — would write to %s\n", path)
		fmt.Printf("%s\n", out)
		return nil
	}
	if err := config.WriteConfigFile(path, doc); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Printf("config imported → %s\n", path)
	return nil
}

func runPluginPublish(bootstrapPath string, args []string) error {
	fs := flag.NewFlagSet("plugin-publish", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var manifestFile string
	var timeoutSec int
	fs.StringVar(&manifestFile, "manifest", "", "path to plugin manifest JSON file (required)")
	fs.IntVar(&timeoutSec, "timeout", 30, "publish timeout seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if manifestFile == "" {
		return fmt.Errorf("plugin-publish requires --manifest")
	}
	cfg, err := config.LoadBootstrap(bootstrapPath)
	if err != nil {
		return fmt.Errorf("load bootstrap: %w", err)
	}
	privateKey, err := config.ResolvePrivateKey(cfg)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(manifestFile)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var m registry.PluginManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("manifest missing \"id\" field")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	reg := registry.NewRegistry(cfg.Relays)
	defer reg.Close()
	eventID, err := reg.Publish(ctx, privateKey, m)
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	fmt.Printf("plugin published id=%s event_id=%s\n", m.ID, eventID)
	return nil
}

func runPluginSearch(bootstrapPath string, args []string) error {
	fs := flag.NewFlagSet("plugin-search", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var query string
	var limit int
	var timeoutSec int
	fs.StringVar(&query, "q", "", "search query (plugin ID or keyword)")
	fs.IntVar(&limit, "limit", 10, "max results")
	fs.IntVar(&timeoutSec, "timeout", 15, "search timeout seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.LoadBootstrap(bootstrapPath)
	if err != nil {
		return fmt.Errorf("load bootstrap: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	reg := registry.NewRegistry(cfg.Relays)
	defer reg.Close()
	results, err := reg.Search(ctx, query, limit)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	if len(results) == 0 {
		fmt.Println("no plugins found")
		return nil
	}
	for _, e := range results {
		author := e.AuthorPubKey
		if len(author) > 12 {
			author = author[:12] + "..."
		}
		fmt.Printf("[%s] v%s by %s — %s\n", e.Manifest.ID, e.Manifest.Version, author, e.Manifest.Description)
	}
	return nil
}

func runPluginInstall(bootstrapPath string, args []string) error {
	fs := flag.NewFlagSet("plugin-install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var pubkey string
	var pluginID string
	var destDir string
	var timeoutSec int
	fs.StringVar(&pubkey, "pubkey", "", "author pubkey (hex or npub, required)")
	fs.StringVar(&pluginID, "id", "", "plugin ID (required)")
	fs.StringVar(&destDir, "dir", "", "install directory (default: ~/.metiq/plugins)")
	fs.IntVar(&timeoutSec, "timeout", 60, "install timeout seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if pubkey == "" || pluginID == "" {
		return fmt.Errorf("plugin-install requires --pubkey and --id")
	}
	if destDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home dir: %w", err)
		}
		destDir = home + "/.metiq/plugins"
	}
	cfg, err := config.LoadBootstrap(bootstrapPath)
	if err != nil {
		return fmt.Errorf("load bootstrap: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	reg := registry.NewRegistry(cfg.Relays)
	defer reg.Close()
	entry, err := reg.Fetch(ctx, pubkey, pluginID)
	if err != nil {
		return fmt.Errorf("fetch plugin: %w", err)
	}
	if entry.Manifest.DownloadURL == "" {
		// Inline plugin — write manifest JSON as index.js stub for manual install.
		pluginDir := destDir + "/" + pluginID
		if err := os.MkdirAll(pluginDir, 0o755); err != nil {
			return fmt.Errorf("create plugin dir: %w", err)
		}
		out, _ := json.MarshalIndent(entry.Manifest, "", "  ")
		if err := os.WriteFile(pluginDir+"/manifest.json", out, 0o600); err != nil {
			return fmt.Errorf("write manifest: %w", err)
		}
		fmt.Printf("plugin fetched (no archive) %s → %s\n", pluginID, pluginDir)
		return nil
	}
	installedPath, err := registry.Install(ctx, *entry, destDir)
	if err != nil {
		return fmt.Errorf("install: %w", err)
	}
	fmt.Printf("plugin installed %s v%s → %s\n", pluginID, entry.Manifest.Version, installedPath)
	return nil
}

func usage() {
	printBanner()
	printVersion(version)
	printBlankLine()
	printInfo("Usage: %s", printCommand("metiq <command> [flags]"))
	printBlankLine()

	groups := currentRegistry().commandsByGroup()
	order := []string{"Daemon Status", "Agent Management", "Channels & Skills", "Config", "Secrets", "Plugins", "Tasks", "Daemon Lifecycle", "Gateway Passthrough", "Migration", "Memory", "Other"}
	for _, group := range order {
		commands := groups[group]
		if len(commands) == 0 {
			continue
		}
		printListHeader(group)
		for _, cmd := range commands {
			if len(cmd.Details) == 0 {
				printMuted("  %-18s %s", cmd.Name, cmd.Summary)
				continue
			}
			for _, detail := range cmd.Details {
				printMuted("  %s", detail)
			}
		}
		printBlankLine()
	}

	printListHeader("Global Flags")
	printMuted("  --admin-addr <host:port>  admin API address")
	printMuted("  --admin-token <token>     admin API bearer token")
	printMuted("  --bootstrap <path>        bootstrap config path")
}
