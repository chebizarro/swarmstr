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

	// run dispatches to a named handler; exits with code 1 on error, 2 on unknown.
	run := func(name string, fn func([]string) error, fnArgs []string) {
		if err := fn(fnArgs); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
			os.Exit(1)
		}
	}

	switch args[0] {
	// ── version ──────────────────────────────────────────────────────────────
	case "version", "--version", "-version":
		run("version", runVersion, args[1:])

	// ── status / health ───────────────────────────────────────────────────────
	case "status":
		run("status", runStatus, args[1:])
	case "health":
		run("health", runHealth, args[1:])

	// ── logs ─────────────────────────────────────────────────────────────────
	case "logs":
		run("logs", runLogs, args[1:])

	// ── models ───────────────────────────────────────────────────────────────
	case "models":
		run("models", runModels, args[1:])

	// ── channels ─────────────────────────────────────────────────────────────
	case "channels":
		run("channels", runChannels, args[1:])

	// ── agents ───────────────────────────────────────────────────────────────
	case "agents":
		run("agents", runAgents, args[1:])

	// ── skills ───────────────────────────────────────────────────────────────
	case "skills":
		run("skills", runSkills, args[1:])

	// ── hooks ────────────────────────────────────────────────────────────────
	case "hooks":
		run("hooks", runHooks, args[1:])

	// ── secrets ──────────────────────────────────────────────────────────────
	case "secrets":
		run("secrets", runSecrets, args[1:])

	// ── update ───────────────────────────────────────────────────────────────
	case "update":
		run("update", runUpdate, args[1:])

	// ── security ─────────────────────────────────────────────────────────────
	case "security":
		run("security", runSecurity, args[1:])

	// ── plugins (rich sub-CLI) ────────────────────────────────────────────────
	case "plugins":
		run("plugins", runPlugins, args[1:])

	// ── config sub-CLI ────────────────────────────────────────────────────────
	case "config":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "config subcommands: get, validate, path, import, export\n")
			os.Exit(2)
		}
		switch args[1] {
		case "get":
			run("config get", runConfigGet, args[2:])
		case "validate":
			run("config validate", runConfigValidate, args[2:])
		case "path":
			run("config path", runConfigPath, args[2:])
		case "import":
			run("config import", runConfigImport, args[2:])
		case "export":
			run("config export", runConfigExport, args[2:])
		default:
			fmt.Fprintf(os.Stderr, "config subcommands: get, validate, path, import, export\n")
			os.Exit(2)
		}

	case "lists", "list":
		run("lists", runLists, args[1:])

	// ── nodes ────────────────────────────────────────────────────────────────
	case "nodes", "node":
		run("nodes", runNodes, args[1:])
	case "sessions", "session":
		run("sessions", runSessions, args[1:])
	case "cron":
		run("cron", runCron, args[1:])
	case "approvals", "approval":
		run("approvals", runApprovals, args[1:])
	case "doctor":
		run("doctor", runDoctor, args[1:])
	case "qr":
		run("qr", runQR, args[1:])
	case "completion":
		run("completion", runCompletion, args[1:])
	case "daemon":
		run("daemon", runDaemon, args[1:])
	case "gw":
		run("gw", runGW, args[1:])

	// ── keygen ───────────────────────────────────────────────────────────────
	case "keygen":
		run("keygen", runKeygen, args[1:])

	// ── legacy flat commands (kept for backward compat) ───────────────────────
	case "plan":
		fmt.Println("docs/PORT_PLAN.md")
	case "init":
		run("init", runInit, args[1:])

	case "bootstrap-check":
		cfg, err := config.LoadBootstrap(bootstrapPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bootstrap invalid: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("bootstrap ok: relays=%d state_kind=%d transcript_kind=%d\n",
			len(cfg.Relays), cfg.EffectiveStateKind(), cfg.EffectiveTranscriptKind())
	case "dm-send":
		if err := runDMSend(bootstrapPath, args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "dm-send failed: %v\n", err)
			os.Exit(1)
		}
	case "memory-search":
		if err := runMemorySearch(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "memory-search failed: %v\n", err)
			os.Exit(1)
		}
	case "config-export":
		if err := runConfigExport(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "config-export failed: %v\n", err)
			os.Exit(1)
		}
	case "config-import":
		if err := runConfigImport(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "config-import failed: %v\n", err)
			os.Exit(1)
		}
	case "plugin-publish":
		if err := runPluginPublish(bootstrapPath, args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "plugin-publish failed: %v\n", err)
			os.Exit(1)
		}
	case "plugin-search":
		if err := runPluginSearch(bootstrapPath, args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "plugin-search failed: %v\n", err)
			os.Exit(1)
		}
	case "plugin-install":
		if err := runPluginInstall(bootstrapPath, args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "plugin-install failed: %v\n", err)
			os.Exit(1)
		}
	default:
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
	fmt.Printf("metiq %s\n\n", version)
	fmt.Println("Usage: metiq <command> [flags]")
	fmt.Println()
	fmt.Println("Daemon status (requires running metiqd with --admin-addr):")
	fmt.Println("  status             show daemon status (pubkey, uptime, relays)")
	fmt.Println("  health             ping daemon health endpoint")
	fmt.Println("  logs               tail recent daemon log lines (--lines N)")
	fmt.Println()
	fmt.Println("Agent management:")
	fmt.Println("  agents list        list configured agents")
	fmt.Println("  models list        list available models (--agent)")
	fmt.Println("  models set <id>    set default model for an agent")
	fmt.Println()
	fmt.Println("Channels & skills:")
	fmt.Println("  channels list      list configured channels and their status")
	fmt.Println("  skills list        list installed skills")
	fmt.Println("  skills status      detailed skills status")
	fmt.Println("  hooks list         list installed hooks")
	fmt.Println()
	fmt.Println("Config:")
	fmt.Println("  config get [key]   get config value (dot-notation key optional)")
	fmt.Println("  config validate    validate live config file")
	fmt.Println("  config path        print config file path")
	fmt.Println("  config import      import config from file (--file --path --dry-run)")
	fmt.Println("  config export      export config (--path --out --redact)")
	fmt.Println("  lists get          read a runtime list doc from Nostr state (--name)")
	fmt.Println("  lists put          write a runtime list doc (--name --item/--file)")
	fmt.Println()
	fmt.Println("Secrets:")
	fmt.Println("  secrets list       list secret keys")
	fmt.Println("  secrets get <key>  get a secret value")
	fmt.Println("  secrets set <k> <v> set a secret value")
	fmt.Println()
	fmt.Println("Plugins:")
	fmt.Println("  plugins list       list installed plugins")
	fmt.Println("  plugins install    install plugin from Nostr (--pubkey --id)")
	fmt.Println("  plugins search     search Nostr plugin registry (--q)")
	fmt.Println("  plugins publish    publish plugin manifest (--manifest)")
	fmt.Println()
	fmt.Println("Daemon lifecycle:")
	fmt.Println("  daemon start       start metiqd in background (--bin --bootstrap)")
	fmt.Println("  daemon stop        send SIGTERM to running daemon")
	fmt.Println("  daemon restart     stop then start daemon")
	fmt.Println("  daemon status      show daemon liveness and uptime")
	fmt.Println()
	fmt.Println("Gateway passthrough:")
	fmt.Println("  gw <method> [params]  call any gateway method and print JSON result")
	fmt.Println("                        params: JSON object or key=value pairs")
	fmt.Println()
	fmt.Println("Other:")
	fmt.Println("  security audit     run local security posture checks")
	fmt.Println("  update             check for daemon updates")
	fmt.Println("  version            print version")
	fmt.Println("  dm-send            send a NIP-17 DM (--to --text)")
	fmt.Println("  memory-search      search local memory index (--q [--limit])")
	fmt.Println("  bootstrap-check    validate bootstrap config")
	fmt.Println()
	fmt.Println("Global flags (for daemon commands):")
	fmt.Println("  --admin-addr <host:port>  admin API address")
	fmt.Println("  --admin-token <token>     admin API bearer token")
	fmt.Println("  --bootstrap <path>        bootstrap config path")
}
