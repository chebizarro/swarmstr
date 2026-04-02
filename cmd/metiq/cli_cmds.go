package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"metiq/internal/config"
	"metiq/internal/security"

	qrcode "github.com/skip2/go-qrcode"
)

// ─── status ───────────────────────────────────────────────────────────────────

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.get("/status")
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(result)
	}

	pubkey := stringField(result, "pubkey")
	uptime := floatField(result, "uptime_seconds")
	dmPolicy := stringField(result, "dm_policy")
	ver := stringField(result, "version")

	fmt.Printf("● metiqd running\n")
	fmt.Printf("  pubkey:    %s\n", pubkey)
	fmt.Printf("  version:   %s\n", ver)
	fmt.Printf("  uptime:    %.0fs\n", uptime)
	fmt.Printf("  dm_policy: %s\n", dmPolicy)

	if relays, ok := result["relays"].([]any); ok {
		fmt.Printf("  relays:    %d\n", len(relays))
		for _, r := range relays {
			fmt.Printf("             %v\n", r)
		}
	}
	return nil
}

// ─── version ─────────────────────────────────────────────────────────────────

func runVersion(_ []string) error {
	fmt.Printf("metiq %s\n", version)
	return nil
}

// ─── logs ────────────────────────────────────────────────────────────────────

func runLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var lines int
	var level string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.IntVar(&lines, "lines", 50, "number of recent log lines to show")
	fs.StringVar(&level, "level", "", "filter by log level (debug|info|warn|error)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("logs.tail", map[string]any{
		"lines": lines,
		"level": level,
	})
	if err != nil {
		return err
	}
	return printJSON(result)
}

// ─── models ──────────────────────────────────────────────────────────────────

func runModels(args []string) error {
	if len(args) == 0 {
		return runModelsList(nil)
	}
	switch args[0] {
	case "list":
		return runModelsList(args[1:])
	case "set":
		return runModelsSet(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "models subcommands: list, set\n")
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func runModelsList(args []string) error {
	fs := flag.NewFlagSet("models list", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, agentID string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&agentID, "agent", "", "agent ID (default: default agent)")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("models.list", map[string]any{"agent_id": agentID})
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(result)
	}

	models, _ := result["models"].([]any)
	if len(models) == 0 {
		fmt.Println("no models available")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tPROVIDER\tCONTEXT")
	for _, m := range models {
		mod, ok := m.(map[string]any)
		if !ok {
			continue
		}
		id := stringField(mod, "id")
		prov := stringField(mod, "provider")
		ctx := ""
		if v, ok := mod["context_window"].(float64); ok {
			ctx = fmt.Sprintf("%dk", int(v/1000))
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", id, prov, ctx)
	}
	return w.Flush()
}

func runModelsSet(args []string) error {
	fs := flag.NewFlagSet("models set", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, agentID string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&agentID, "agent", "", "agent ID (default: default agent)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: metiq models set <model-id> [--agent <id>]")
	}
	modelID := fs.Arg(0)

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	_, err = cl.call("agents.update", map[string]any{
		"agent_id": agentID,
		"model":    modelID,
	})
	if err != nil {
		return err
	}
	fmt.Printf("default model set to: %s\n", modelID)
	return nil
}

// ─── channels ────────────────────────────────────────────────────────────────

func runChannels(args []string) error {
	if len(args) == 0 {
		return runChannelsList(nil)
	}
	switch args[0] {
	case "list", "ls":
		return runChannelsList(args[1:])
	case "status":
		return runChannelsStatus(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "channels subcommands: list, status\n")
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func runChannelsList(args []string) error {
	fs := flag.NewFlagSet("channels list", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("channels.status", map[string]any{})
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(result)
	}

	chans, _ := result["channels"].([]any)
	if len(chans) == 0 {
		fmt.Println("no channels configured")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tKIND\tSTATUS")
	for _, c := range chans {
		ch, ok := c.(map[string]any)
		if !ok {
			continue
		}
		id := stringField(ch, "id")
		kind := stringField(ch, "kind")
		if kind == "" {
			kind = stringFieldAny(ch, "channel")
		}
		if kind == "" {
			kind = id
		}
		status := stringField(ch, "status")
		if status == "" {
			status = channelStatusLabel(ch)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", id, kind, status)
	}
	return w.Flush()
}

func runChannelsStatus(args []string) error {
	fs := flag.NewFlagSet("channels status", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("channels.status", map[string]any{})
	if err != nil {
		return err
	}
	return printJSON(result)
}

// ─── skills ───────────────────────────────────────────────────────────────────

func runSkills(args []string) error {
	if len(args) == 0 {
		return runSkillsList(nil)
	}
	switch args[0] {
	case "list", "ls":
		return runSkillsList(args[1:])
	case "status":
		return runSkillsStatus(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "skills subcommands: list, status\n")
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func runSkillsList(args []string) error {
	fs := flag.NewFlagSet("skills list", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("skills.status", map[string]any{})
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(result)
	}

	skills, _ := result["skills"].([]any)
	managed, _ := result["managedSkills"].([]any)
	all := append(skills, managed...)

	if len(all) == 0 {
		fmt.Println("no skills installed")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tDESCRIPTION")
	for _, s := range all {
		sk, ok := s.(map[string]any)
		if !ok {
			continue
		}
		id := stringField(sk, "id")
		status := stringField(sk, "status")
		desc := stringField(sk, "description")
		if len(desc) > 60 {
			desc = desc[:57] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", id, status, desc)
	}
	return w.Flush()
}

func runSkillsStatus(args []string) error {
	fs := flag.NewFlagSet("skills status", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("skills.status", map[string]any{})
	if err != nil {
		return err
	}
	return printJSON(result)
}

// ─── hooks ────────────────────────────────────────────────────────────────────

func runHooks(args []string) error {
	if len(args) == 0 {
		return runHooksList(nil)
	}
	switch args[0] {
	case "list", "ls":
		return runHooksList(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "hooks subcommands: list\n")
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func runHooksList(args []string) error {
	fs := flag.NewFlagSet("hooks list", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("hooks.list", map[string]any{})
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(result)
	}

	hooks, _ := result["hooks"].([]any)
	if len(hooks) == 0 {
		fmt.Println("no hooks installed")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tENABLED\tDESCRIPTION")
	for _, h := range hooks {
		hk, ok := h.(map[string]any)
		if !ok {
			continue
		}
		id := stringField(hk, "id")
		desc := stringField(hk, "description")
		enabled := "yes"
		if v, ok := hk["enabled"].(bool); ok && !v {
			enabled = "no"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", id, enabled, desc)
	}
	return w.Flush()
}

// ─── secrets ─────────────────────────────────────────────────────────────────

func runSecrets(args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "secrets subcommands: list, get, set\n")
		return fmt.Errorf("missing subcommand")
	}
	switch args[0] {
	case "list", "ls":
		return runSecretsList(args[1:])
	case "get":
		return runSecretsGet(args[1:])
	case "set":
		return runSecretsSet(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "secrets subcommands: list, get, set\n")
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func runSecretsList(args []string) error {
	fs := flag.NewFlagSet("secrets list", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("secrets.reload", map[string]any{})
	if err != nil {
		return err
	}

	count := 0
	if v, ok := result["count"].(float64); ok {
		count = int(v)
	}
	warningCount := 0
	if v, ok := result["warningCount"].(float64); ok {
		warningCount = int(v)
	}
	fmt.Printf("secrets reloaded: %d (warnings: %d)\n", count, warningCount)
	if warnings, ok := result["warnings"].([]any); ok {
		for _, w := range warnings {
			if s, ok := w.(string); ok && strings.TrimSpace(s) != "" {
				fmt.Printf("- %s\n", s)
			}
		}
	}
	return nil
}

func runSecretsGet(args []string) error {
	fs := flag.NewFlagSet("secrets get", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: metiq secrets get <key>")
	}
	key := fs.Arg(0)

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("secrets.resolve", map[string]any{
		"targetIds": []string{"env:" + key},
	})
	if err != nil {
		return err
	}

	assignments, _ := result["assignments"].([]any)
	if len(assignments) == 0 {
		fmt.Fprintf(os.Stderr, "secret %q not found\n", key)
		os.Exit(1)
	}
	first, _ := assignments[0].(map[string]any)
	found, _ := first["found"].(bool)
	if !found {
		fmt.Fprintf(os.Stderr, "secret %q not found\n", key)
		os.Exit(1)
	}
	if v, ok := first["value"].(string); ok {
		fmt.Println(v)
		return nil
	}
	fmt.Fprintf(os.Stderr, "secret %q not found\n", key)
	os.Exit(1)
	return nil
}

func runSecretsSet(args []string) error {
	fs := flag.NewFlagSet("secrets set", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: metiq secrets set <key> <value>")
	}
	key := fs.Arg(0)
	value := fs.Arg(1)

	_ = value
	return fmt.Errorf("secrets set is not supported by the daemon API; set %q in your environment or .env and run `metiq secrets list` (reload)", key)
}

// ─── update ───────────────────────────────────────────────────────────────────

func runUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("update.status", map[string]any{})
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(result)
	}

	current := stringField(result, "current_version")
	latest := stringField(result, "latest_version")
	hasUpdate, _ := result["has_update"].(bool)

	fmt.Printf("current: %s\n", current)
	fmt.Printf("latest:  %s\n", latest)
	if hasUpdate {
		fmt.Printf("update available — run: curl -fsSL https://raw.githubusercontent.com/metiq/metiq/main/scripts/install.sh | bash\n")
	} else {
		fmt.Println("up to date")
	}
	return nil
}

// ─── security ────────────────────────────────────────────────────────────────

func runSecurity(args []string) error {
	if len(args) == 0 {
		return runSecurityAudit(nil)
	}
	switch args[0] {
	case "audit":
		return runSecurityAudit(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "security subcommands: audit\n")
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func runSecurityAudit(args []string) error {
	fs := flag.NewFlagSet("security audit", flag.ContinueOnError)
	var bootstrapPath, configPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&configPath, "config", "", "live config path")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	findings := collectSecurityFindings(bootstrapPath, configPath)

	if jsonOut {
		return printJSON(map[string]any{"findings": findings})
	}

	if len(findings) == 0 {
		fmt.Println("✓ No security issues found")
		return nil
	}

	severityOrder := map[string]int{"critical": 0, "warn": 1, "info": 2}
	sort.Slice(findings, func(i, j int) bool {
		si := severityOrder[findings[i].Severity]
		sj := severityOrder[findings[j].Severity]
		if si != sj {
			return si < sj
		}
		return findings[i].CheckID < findings[j].CheckID
	})

	critical := 0
	warns := 0
	for _, f := range findings {
		icon := "·"
		switch f.Severity {
		case "critical":
			icon = "✗"
			critical++
		case "warn":
			icon = "!"
			warns++
		}
		fmt.Printf("%s [%s] %s: %s\n", icon, f.Severity, f.CheckID, f.Message)
		if f.Remediation != "" {
			fmt.Printf("  → %s\n", f.Remediation)
		}
	}

	fmt.Printf("\n%d findings (%d critical, %d warn)\n", len(findings), critical, warns)
	if critical > 0 {
		return fmt.Errorf("security audit failed: %d critical issue(s)", critical)
	}
	return nil
}

// securityFinding aliases the security package type for CLI rendering.
type securityFinding = security.Finding

// collectSecurityFindings runs security checks using the internal security package.
func collectSecurityFindings(bootstrapPath, _ string) []securityFinding {
	report := security.Audit(security.AuditOptions{
		BootstrapPath: bootstrapPath,
	})
	return report.Findings
}

// ─── config subcommands ───────────────────────────────────────────────────────

func runConfigGet(args []string) error {
	fs := flag.NewFlagSet("config get", flag.ContinueOnError)
	var configPath string
	var jsonOut bool
	fs.StringVar(&configPath, "path", "", "config file path")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if configPath == "" {
		var err error
		configPath, err = config.DefaultConfigPath()
		if err != nil {
			return fmt.Errorf("resolve default config path: %w", err)
		}
	}

	doc, err := config.LoadConfigFile(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// If a key was given, extract it; otherwise print the whole config.
	if key := fs.Arg(0); key != "" {
		raw, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		// Walk dot-separated keys.
		parts := strings.Split(key, ".")
		var cur any = m
		for _, p := range parts {
			mm, ok := cur.(map[string]any)
			if !ok {
				fmt.Fprintf(os.Stderr, "key %q not found\n", key)
				os.Exit(1)
			}
			cur, ok = mm[p]
			if !ok {
				fmt.Fprintf(os.Stderr, "key %q not found\n", key)
				os.Exit(1)
			}
		}
		if s, ok := cur.(string); ok && !jsonOut {
			fmt.Println(s)
			return nil
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(cur)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

func runConfigValidate(args []string) error {
	fs := flag.NewFlagSet("config validate", flag.ContinueOnError)
	var configPath string
	fs.StringVar(&configPath, "path", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if configPath == "" {
		var err error
		configPath, err = config.DefaultConfigPath()
		if err != nil {
			return fmt.Errorf("resolve default config path: %w", err)
		}
	}

	doc, err := config.LoadConfigFile(configPath)
	if err != nil {
		return fmt.Errorf("config invalid: %w", err)
	}

	if errs := config.ValidateConfigDoc(doc); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  %v\n", e)
		}
		return fmt.Errorf("config has %d validation error(s)", len(errs))
	}

	fmt.Printf("config valid: %s\n", configPath)
	return nil
}

func runConfigPath(args []string) error {
	fs := flag.NewFlagSet("config path", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := config.DefaultConfigPath()
	if err != nil {
		return err
	}
	fmt.Println(p)
	return nil
}

func runLists(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("lists subcommands: get, put")
	}
	switch args[0] {
	case "get":
		return runListsGet(args[1:])
	case "put":
		return runListsPut(args[1:])
	default:
		return fmt.Errorf("unknown lists sub-command %q (get|put)", args[0])
	}
}

func runListsGet(args []string) error {
	fs := flag.NewFlagSet("lists get", flag.ContinueOnError)
	var name string
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&name, "name", "", "list name")
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("lists get requires --name")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("list.get", map[string]any{"name": name})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	listDoc, _ := result["list"].(map[string]any)
	if listDoc == nil {
		return printJSON(result)
	}
	items, _ := listDoc["items"].([]any)
	fmt.Printf("list=%s items=%d\n", stringField(listDoc, "name"), len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			fmt.Println(s)
		}
	}
	return nil
}

func runListsPut(args []string) error {
	fs := flag.NewFlagSet("lists put", flag.ContinueOnError)
	var name string
	var itemsCSV string
	var itemsFile string
	var expectedVersion int
	var expectedEvent string
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&name, "name", "", "list name")
	fs.StringVar(&itemsCSV, "item", "", "comma-separated list items")
	fs.StringVar(&itemsFile, "file", "", "newline-delimited list items file")
	fs.IntVar(&expectedVersion, "expected-version", -1, "optimistic version precondition")
	fs.StringVar(&expectedEvent, "expected-event", "", "optimistic expected event id")
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("lists put requires --name")
	}
	itemsSet := map[string]struct{}{}
	items := make([]string, 0)
	for _, part := range strings.Split(itemsCSV, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, exists := itemsSet[part]; exists {
			continue
		}
		itemsSet[part] = struct{}{}
		items = append(items, part)
	}
	if strings.TrimSpace(itemsFile) != "" {
		raw, err := os.ReadFile(itemsFile)
		if err != nil {
			return fmt.Errorf("read items file: %w", err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if _, exists := itemsSet[line]; exists {
				continue
			}
			itemsSet[line] = struct{}{}
			items = append(items, line)
		}
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	params := map[string]any{"name": name, "items": items}
	if expectedVersion >= 0 {
		params["expected_version"] = expectedVersion
	}
	if strings.TrimSpace(expectedEvent) != "" {
		params["expected_event"] = strings.TrimSpace(expectedEvent)
	}
	result, err := cl.call("list.put", params)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	if eventID := stringField(result, "event_id"); eventID != "" {
		fmt.Printf("list=%s updated event_id=%s items=%d\n", name, eventID, len(items))
		return nil
	}
	return printJSON(result)
}

// ─── agents ───────────────────────────────────────────────────────────────────

func runAgents(args []string) error {
	if len(args) == 0 {
		return runAgentsList(nil)
	}
	switch args[0] {
	case "list", "ls":
		return runAgentsList(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "agents subcommands: list\n")
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func runAgentsList(args []string) error {
	fs := flag.NewFlagSet("agents list", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("agents.list", map[string]any{})
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(result)
	}

	agents, _ := result["agents"].([]any)
	if len(agents) == 0 {
		fmt.Println("no agents configured")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tMODEL\tSTATUS")
	for _, a := range agents {
		ag, ok := a.(map[string]any)
		if !ok {
			continue
		}
		id := stringField(ag, "id")
		model := stringField(ag, "model")
		status := stringField(ag, "status")
		fmt.Fprintf(w, "%s\t%s\t%s\n", id, model, status)
	}
	return w.Flush()
}

// ─── plugins (richer CLI wrappers) ───────────────────────────────────────────

func runPlugins(args []string) error {
	if len(args) == 0 {
		return runPluginsList(nil)
	}
	switch args[0] {
	case "list", "ls":
		return runPluginsList(args[1:])
	case "install":
		return runPluginInstall("", args[1:])
	case "search":
		return runPluginSearch("", args[1:])
	case "publish":
		return runPluginPublish("", args[1:])
	default:
		fmt.Fprintf(os.Stderr, "plugins subcommands: list, install, search, publish\n")
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func runPluginsList(args []string) error {
	fs := flag.NewFlagSet("plugins list", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("plugins.list", map[string]any{})
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(result)
	}

	plugins, _ := result["plugins"].([]any)
	if len(plugins) == 0 {
		fmt.Println("no plugins installed")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tVERSION\tSTATUS")
	for _, p := range plugins {
		pl, ok := p.(map[string]any)
		if !ok {
			continue
		}
		id := stringField(pl, "id")
		ver := stringField(pl, "version")
		status := stringField(pl, "status")
		fmt.Fprintf(w, "%s\t%s\t%s\n", id, ver, status)
	}
	return w.Flush()
}

// ─── health ───────────────────────────────────────────────────────────────────

func runHealth(args []string) error {
	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.get("/health")
	if err != nil {
		fmt.Fprintln(os.Stderr, "daemon unreachable:", err)
		os.Exit(1)
	}
	if ok, _ := result["ok"].(bool); ok {
		fmt.Println("ok")
	} else {
		fmt.Fprintln(os.Stderr, "daemon returned unhealthy status")
		os.Exit(1)
	}
	return nil
}

// ─── nodes ────────────────────────────────────────────────────────────────────

// runNodesList lists known remote metiq nodes via the daemon's node.list method.
func runNodesList(args []string) error {
	fs := flag.NewFlagSet("nodes list", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("node.list", map[string]any{})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	nodes, _ := result["nodes"].([]any)
	if len(nodes) == 0 {
		fmt.Println("No remote nodes registered.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NODE ID\tNAME\tSTATUS")
	for _, n := range nodes {
		node, ok := n.(map[string]any)
		if !ok {
			continue
		}
		id := stringFieldAny(node, "node_id")
		name := stringFieldAny(node, "name")
		status := stringFieldAny(node, "status")
		if status == "" {
			status = "unknown"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", id, name, status)
	}
	return w.Flush()
}

// runNodesAdd adds a remote node by Nostr pubkey (hex or npub).
func runNodesAdd(args []string) error {
	fs := flag.NewFlagSet("nodes add", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, name string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&name, "name", "", "human-readable name for this node")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq nodes add <npub|hex-pubkey> [--name <label>]")
	}
	pubkey := fs.Arg(0)

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	params := map[string]any{"node_id": pubkey}
	if name != "" {
		params["name"] = name
	}
	result, err := cl.call("node.describe", params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Note: node %q not yet known to daemon (%v).\n", pubkey, err)
		fmt.Fprintf(os.Stderr, "Add it to the 'nodes' section of your config file and restart.\n")
		return nil
	}
	return printJSON(result)
}

// runNodesStatus pings a remote node and reports its status.
func runNodesStatus(args []string) error {
	fs := flag.NewFlagSet("nodes status", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq nodes status <npub|hex-pubkey>")
	}
	nodeID := fs.Arg(0)

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("node.describe", map[string]any{"node_id": nodeID})
	if err != nil {
		return fmt.Errorf("node.describe: %w", err)
	}
	if jsonOut {
		return printJSON(result)
	}
	statusStr := stringFieldAny(result, "status")
	name := stringFieldAny(result, "name")
	fmt.Printf("Node:   %s\n", nodeID)
	if name != "" {
		fmt.Printf("Name:   %s\n", name)
	}
	fmt.Printf("Status: %s\n", statusStr)
	return nil
}

// runNodesSend sends a DM to a remote metiq node.
func runNodesSend(args []string) error {
	fs := flag.NewFlagSet("nodes send", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: metiq nodes send <npub|hex-pubkey> <message>")
	}
	to := fs.Arg(0)
	message := strings.Join(fs.Args()[1:], " ")

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("chat.send", map[string]any{"to": to, "message": message})
	if err != nil {
		return fmt.Errorf("chat.send: %w", err)
	}
	return printJSON(result)
}

// runNodes dispatches nodes sub-commands.
func runNodes(args []string) error {
	if len(args) == 0 {
		return runNodesList(args)
	}
	switch args[0] {
	case "list", "ls":
		return runNodesList(args[1:])
	case "add":
		return runNodesAdd(args[1:])
	case "status":
		return runNodesStatus(args[1:])
	case "send":
		return runNodesSend(args[1:])
	case "pending":
		return runNodesPending(args[1:])
	case "approve":
		return runNodesApprove(args[1:])
	case "reject":
		return runNodesReject(args[1:])
	case "describe":
		return runNodesDescribe(args[1:])
	case "invoke":
		return runNodesInvoke(args[1:])
	case "rename":
		return runNodesRename(args[1:])
	default:
		return fmt.Errorf("unknown nodes sub-command %q (list|add|status|send|pending|approve|reject|describe|invoke|rename)", args[0])
	}
}

// runNodesPending lists pending node pairing requests.
func runNodesPending(args []string) error {
	fs := flag.NewFlagSet("nodes pending", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut, includePaired bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	fs.BoolVar(&includePaired, "include-paired", false, "also print currently paired nodes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("node.pair.list", map[string]any{})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	pending, _ := result["pending"].([]any)
	paired, _ := result["paired"].([]any)
	if len(pending) == 0 && (!includePaired || len(paired) == 0) {
		fmt.Println("No pending node pairing requests.")
		return nil
	}
	if len(pending) > 0 {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "REQUEST ID\tNODE ID\tSTATUS")
		for _, r := range pending {
			req, ok := r.(map[string]any)
			if !ok {
				continue
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n",
				stringFieldAny(req, "request_id"),
				stringFieldAny(req, "node_id"),
				stringFieldAny(req, "status"),
			)
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}
	if includePaired {
		if len(paired) > 0 {
			if len(pending) > 0 {
				fmt.Println()
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PAIRED NODE ID\tDISPLAY NAME\tAPPROVED AT")
			for _, r := range paired {
				node, ok := r.(map[string]any)
				if !ok {
					continue
				}
				fmt.Fprintf(w, "%s\t%s\t%d\n",
					stringFieldAny(node, "node_id"),
					stringFieldAny(node, "display_name"),
					int64(floatFieldAny(node, "approved_at_ms")),
				)
			}
			if err := w.Flush(); err != nil {
				return err
			}
		} else if len(pending) == 0 {
			fmt.Println("No paired nodes.")
		}
	}
	return nil
}

// runNodesApprove approves a pending node pairing request.
func runNodesApprove(args []string) error {
	fs := flag.NewFlagSet("nodes approve", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq nodes approve <request-id>")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("node.pair.approve", map[string]any{"request_id": fs.Arg(0)})
	if err != nil {
		return err
	}
	return printJSON(result)
}

// runNodesReject rejects a pending node pairing request.
func runNodesReject(args []string) error {
	fs := flag.NewFlagSet("nodes reject", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq nodes reject <request-id>")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("node.pair.reject", map[string]any{"request_id": fs.Arg(0)})
	if err != nil {
		return err
	}
	return printJSON(result)
}

// runNodesDescribe shows detailed info about a node.
func runNodesDescribe(args []string) error {
	fs := flag.NewFlagSet("nodes describe", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq nodes describe <node-id>")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("node.describe", map[string]any{"node_id": fs.Arg(0)})
	if err != nil {
		return err
	}
	return printJSON(result)
}

// runNodesInvoke invokes a command on a remote node.
func runNodesInvoke(args []string) error {
	fs := flag.NewFlagSet("nodes invoke", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, nodeID, command, rawArgs string
	var timeoutSeconds int
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&nodeID, "node", "", "node ID (required)")
	fs.StringVar(&command, "command", "", "command to invoke (required)")
	fs.StringVar(&rawArgs, "args", "", "JSON args to pass to the command")
	fs.IntVar(&timeoutSeconds, "timeout", 30, "timeout in seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if nodeID == "" || command == "" {
		return fmt.Errorf("usage: metiq nodes invoke --node <id> --command <cmd> [--args '{...}']")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	body := map[string]any{
		"node_id":    nodeID,
		"command":    command,
		"timeout_ms": timeoutSeconds * 1000,
	}
	if rawArgs != "" {
		var argsMap map[string]any
		if err := json.Unmarshal([]byte(rawArgs), &argsMap); err != nil {
			return fmt.Errorf("invalid --args JSON: %w", err)
		}
		body["args"] = argsMap
	}
	result, err := cl.call("node.invoke", body)
	if err != nil {
		return err
	}
	return printJSON(result)
}

// runNodesRename renames a remote node.
func runNodesRename(args []string) error {
	fs := flag.NewFlagSet("nodes rename", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: metiq nodes rename <node-id> <new-name>")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("node.rename", map[string]any{
		"node_id": fs.Arg(0),
		"name":    fs.Arg(1),
	})
	if err != nil {
		return err
	}
	return printJSON(result)
}

// stringFieldAny is like stringField but operates on map[string]any.
func stringFieldAny(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func boolFieldAny(m map[string]any, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func channelStatusLabel(m map[string]any) string {
	if status := stringFieldAny(m, "status"); status != "" {
		return status
	}
	if boolFieldAny(m, "logged_out") {
		return "logged_out"
	}
	if boolFieldAny(m, "connected") {
		return "connected"
	}
	return "disconnected"
}

// ─── sessions ─────────────────────────────────────────────────────────────────

func runSessions(args []string) error {
	if len(args) == 0 {
		return runSessionsList(args)
	}
	switch args[0] {
	case "list", "ls":
		return runSessionsList(args[1:])
	case "get", "show":
		return runSessionsGet(args[1:])
	case "export":
		return runSessionsExport(args[1:])
	case "delete", "rm":
		return runSessionsDelete(args[1:])
	case "reset":
		return runSessionsReset(args[1:])
	case "prune":
		return runSessionsPrune(args[1:])
	default:
		return fmt.Errorf("unknown sessions sub-command %q (list|get|export|delete|reset|prune)", args[0])
	}
}

func runSessionsList(args []string) error {
	fs := flag.NewFlagSet("sessions list", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var limit int
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.IntVar(&limit, "limit", 20, "max sessions to show")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("sessions.list", map[string]any{"limit": limit})
	if err != nil {
		return fmt.Errorf("sessions.list: %w", err)
	}
	if jsonOut {
		return printJSON(result)
	}
	sessions, _ := result["sessions"].([]any)
	if len(sessions) == 0 {
		fmt.Println("no sessions found")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tAGENT\tLAST INBOUND\tLAST REPLY")
	for _, s := range sessions {
		sm, _ := s.(map[string]any)
		sid := stringFieldAny(sm, "session_id")
		agentID := stringFieldAny(sm, "agent_id")
		if agentID == "" {
			agentID = "-"
		}
		lastIn := floatFieldAny(sm, "last_inbound_at")
		lastReply := floatFieldAny(sm, "last_reply_at")
		var lastInStr, lastReplyStr string
		if lastIn > 0 {
			lastInStr = time.Unix(int64(lastIn), 0).Format("2006-01-02 15:04")
		} else {
			lastInStr = "-"
		}
		if lastReply > 0 {
			lastReplyStr = time.Unix(int64(lastReply), 0).Format("2006-01-02 15:04")
		} else {
			lastReplyStr = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", sid, agentID, lastInStr, lastReplyStr)
	}
	return tw.Flush()
}

func runSessionsGet(args []string) error {
	fs := flag.NewFlagSet("sessions get", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: metiq sessions get <session-id>")
	}
	sessionID := fs.Arg(0)
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("session.get", map[string]any{"session_id": sessionID})
	if err != nil {
		return fmt.Errorf("session.get: %w", err)
	}
	return printJSON(result)
}

func runSessionsExport(args []string) error {
	fs := flag.NewFlagSet("sessions export", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, output, format string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.StringVar(&output, "output", "", "output file path (default: stdout)")
	fs.StringVar(&format, "format", "html", "export format (html)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: metiq sessions export <session-id> [--output path]")
	}
	sessionID := fs.Arg(0)
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("sessions.export", map[string]any{
		"session_id": sessionID,
		"format":     format,
	})
	if err != nil {
		return fmt.Errorf("sessions.export: %w", err)
	}
	html, _ := result["html"].(string)
	if html == "" {
		return fmt.Errorf("sessions.export: no content returned")
	}
	if output != "" {
		return os.WriteFile(output, []byte(html), 0o644)
	}
	fmt.Print(html)
	return nil
}

func runSessionsDelete(args []string) error {
	fs := flag.NewFlagSet("sessions delete", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: metiq sessions delete <session-id>")
	}
	sessionID := fs.Arg(0)
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("sessions.delete", map[string]any{"session_id": sessionID})
	if err != nil {
		return fmt.Errorf("sessions.delete: %w", err)
	}
	deleted, _ := result["deleted"].(bool)
	if deleted {
		fmt.Printf("session %s deleted\n", sessionID)
	} else {
		return printJSON(result)
	}
	return nil
}

func runSessionsReset(args []string) error {
	fs := flag.NewFlagSet("sessions reset", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: metiq sessions reset <session-id>")
	}
	sessionID := fs.Arg(0)
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("sessions.reset", map[string]any{"session_id": sessionID})
	if err != nil {
		return fmt.Errorf("sessions.reset: %w", err)
	}
	return printJSON(result)
}

func runSessionsPrune(args []string) error {
	fs := flag.NewFlagSet("sessions prune", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var olderThanStr string
	var dryRun, all bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.StringVar(&olderThanStr, "older-than", "", "delete sessions older than this duration, e.g. 7d, 30d")
	fs.BoolVar(&dryRun, "dry-run", false, "report what would be deleted without deleting")
	fs.BoolVar(&all, "all", false, "delete all sessions regardless of age")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !all && olderThanStr == "" {
		return fmt.Errorf("usage: metiq sessions prune --older-than <Nd> [--dry-run]\n  or:  metiq sessions prune --all [--dry-run]")
	}

	olderThanDays := 0
	if olderThanStr != "" {
		olderThanStr = strings.TrimSuffix(strings.TrimSpace(olderThanStr), "d")
		n, err := strconv.Atoi(olderThanStr)
		if err != nil || n <= 0 {
			return fmt.Errorf("--older-than must be a positive number of days, e.g. 7d (got %q)", olderThanStr)
		}
		olderThanDays = n
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	params := map[string]any{
		"older_than_days": olderThanDays,
		"dry_run":         dryRun,
		"all":             all,
	}
	result, err := cl.call("sessions.prune", params)
	if err != nil {
		return fmt.Errorf("sessions.prune: %w", err)
	}
	deleted, _ := result["deleted_count"].(float64)
	dryRunResult, _ := result["dry_run"].(bool)
	if dryRunResult {
		fmt.Printf("dry-run: would delete %d session(s)\n", int(deleted))
		if ids, ok := result["deleted"].([]any); ok {
			for _, id := range ids {
				fmt.Printf("  %v\n", id)
			}
		}
	} else {
		fmt.Printf("deleted %d session(s)\n", int(deleted))
	}
	return nil
}

// ─── approvals ────────────────────────────────────────────────────────────────

func runApprovals(args []string) error {
	if len(args) == 0 {
		return runApprovalsList(args)
	}
	switch args[0] {
	case "list", "ls":
		return runApprovalsList(args[1:])
	case "approve":
		return runApprovalsResolve(args[1:], "approved")
	case "deny", "reject":
		return runApprovalsResolve(args[1:], "denied")
	default:
		return fmt.Errorf("unknown approvals sub-command %q (list|approve|deny)", args[0])
	}
}

func runApprovalsList(args []string) error {
	fs := flag.NewFlagSet("approvals list", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("exec.approvals.get", map[string]any{})
	if err != nil {
		return fmt.Errorf("exec.approvals.get: %w", err)
	}
	if jsonOut {
		return printJSON(result)
	}
	pending, _ := result["pending"].([]any)
	if len(pending) == 0 {
		fmt.Println("no pending approvals")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSESSION\tCOMMAND\tCREATED")
	for _, p := range pending {
		pm, _ := p.(map[string]any)
		id := stringFieldAny(pm, "id")
		sess := stringFieldAny(pm, "session_id")
		cmd := stringFieldAny(pm, "command")
		created := floatFieldAny(pm, "created_at")
		createdStr := "-"
		if created > 0 {
			createdStr = time.Unix(int64(created), 0).Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", id, sess, cmd, createdStr)
	}
	return tw.Flush()
}

func runApprovalsResolve(args []string, decision string) error {
	fs := flag.NewFlagSet("approvals "+decision, flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: metiq approvals %s <approval-id>", decision)
	}
	approvalID := fs.Arg(0)
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("exec.approval.resolve", map[string]any{
		"id":       approvalID,
		"decision": decision,
	})
	if err != nil {
		return fmt.Errorf("exec.approval.resolve: %w", err)
	}
	return printJSON(result)
}

// ─── cron ─────────────────────────────────────────────────────────────────────

func runCron(args []string) error {
	if len(args) == 0 {
		return runCronList(args)
	}
	switch args[0] {
	case "list", "ls":
		return runCronList(args[1:])
	case "add":
		return runCronAdd(args[1:])
	case "remove", "rm", "delete":
		return runCronRemove(args[1:])
	case "run":
		return runCronRun(args[1:])
	default:
		return fmt.Errorf("unknown cron sub-command %q (list|add|remove|run)", args[0])
	}
}

func runCronList(args []string) error {
	fs := flag.NewFlagSet("cron list", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("cron.list", map[string]any{})
	if err != nil {
		return fmt.Errorf("cron.list: %w", err)
	}
	if jsonOut {
		return printJSON(result)
	}
	jobs, _ := result["jobs"].([]any)
	if len(jobs) == 0 {
		fmt.Println("no cron jobs configured")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSCHEDULE\tENABLED\tLAST RUN\tDESCRIPTION")
	for _, j := range jobs {
		jm, _ := j.(map[string]any)
		id := stringFieldAny(jm, "id")
		schedule := stringFieldAny(jm, "schedule")
		enabled := fmt.Sprintf("%v", jm["enabled"])
		desc := stringFieldAny(jm, "description")
		lastRun := floatFieldAny(jm, "last_run_at")
		lastRunStr := "-"
		if lastRun > 0 {
			lastRunStr = time.Unix(int64(lastRun), 0).Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", id, schedule, enabled, lastRunStr, desc)
	}
	return tw.Flush()
}

func runCronAdd(args []string) error {
	fs := flag.NewFlagSet("cron add", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, id, schedule, message, agentID, method, rawParams string
	var enabled bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.StringVar(&id, "id", "", "cron job ID (auto-generated if empty)")
	fs.StringVar(&schedule, "schedule", "", "cron schedule expression (required); e.g. \"0 7 * * *\", \"@every 1h\", \"@daily\"")
	fs.StringVar(&message, "message", "", "agent message to send on trigger (shorthand: sets method=agent and params.text=<message>)")
	fs.StringVar(&agentID, "agent", "main", "agent ID to target (used with --message)")
	fs.StringVar(&method, "method", "", "gateway method to call on trigger (e.g. \"agent\"); required if --message not set")
	fs.StringVar(&rawParams, "params", "", "JSON params for the method (e.g. '{\"text\":\"hello\"}')")
	fs.BoolVar(&enabled, "enabled", true, "enable job immediately")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if schedule == "" {
		return fmt.Errorf("--schedule is required")
	}
	// --message is a convenience shorthand that maps to method=agent with text param.
	if message != "" && method == "" {
		method = "agent"
		params := map[string]any{"text": message}
		if agentID != "" && agentID != "main" {
			params["session_id"] = agentID
		}
		b, _ := json.Marshal(params)
		rawParams = string(b)
	}
	if method == "" {
		return fmt.Errorf("--method or --message is required")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	body := map[string]any{
		"id":       id,
		"schedule": schedule,
		"method":   method,
		"enabled":  enabled,
	}
	if rawParams != "" {
		body["params"] = json.RawMessage(rawParams)
	}
	result, err := cl.call("cron.add", body)
	if err != nil {
		return fmt.Errorf("cron.add: %w", err)
	}
	return printJSON(result)
}

func runCronRemove(args []string) error {
	fs := flag.NewFlagSet("cron remove", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: metiq cron remove <job-id>")
	}
	jobID := fs.Arg(0)
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("cron.remove", map[string]any{"id": jobID})
	if err != nil {
		return fmt.Errorf("cron.remove: %w", err)
	}
	return printJSON(result)
}

func runCronRun(args []string) error {
	fs := flag.NewFlagSet("cron run", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: metiq cron run <job-id>")
	}
	jobID := fs.Arg(0)
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("cron.run", map[string]any{"id": jobID})
	if err != nil {
		return fmt.Errorf("cron.run: %w", err)
	}
	return printJSON(result)
}

// ─── doctor ───────────────────────────────────────────────────────────────────

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	type check struct {
		name string
		ok   bool
		msg  string
	}
	var checks []check

	// Check: admin reachable.
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		checks = append(checks, check{"admin reachable", false, err.Error()})
	} else {
		result, err := cl.get("/health")
		if err != nil {
			checks = append(checks, check{"admin reachable", false, err.Error()})
		} else {
			checks = append(checks, check{"admin reachable", true, stringFieldAny(result, "status")})
		}
	}

	// Check: bootstrap file exists.
	if bootstrapPath == "" {
		if home, err2 := os.UserHomeDir(); err2 == nil {
			bootstrapPath = home + "/.config/metiq/bootstrap.json"
		}
	}
	if bootstrapPath != "" {
		if _, err2 := os.Stat(bootstrapPath); err2 == nil {
			checks = append(checks, check{"bootstrap file", true, bootstrapPath})
		} else {
			checks = append(checks, check{"bootstrap file", false, "not found at " + bootstrapPath})
		}
	}

	// Check: memory usage (from admin if reachable).
	if cl != nil {
		if result, err := cl.call("doctor.memory.status", map[string]any{}); err == nil {
			if index, ok := result["index"].(map[string]any); ok {
				docs := floatFieldAny(index, "entry_count")
				sessions := floatFieldAny(index, "session_count")
				checks = append(checks, check{"memory index", true, fmt.Sprintf("%.0f docs / %.0f sessions", docs, sessions)})
			} else {
				docs := floatFieldAny(result, "doc_count")
				checks = append(checks, check{"memory index", true, fmt.Sprintf("%.0f docs", docs)})
			}
		}
	}

	// Check: relay connectivity (from status).
	if cl != nil {
		if result, err := cl.get("/status"); err == nil {
			if relays, ok := result["relays"].([]any); ok {
				checks = append(checks, check{"relay config", len(relays) > 0,
					fmt.Sprintf("%d relay(s) configured", len(relays))})
			}
		}
	}

	if jsonOut {
		out := make([]map[string]any, len(checks))
		for i, c := range checks {
			out[i] = map[string]any{"name": c.name, "ok": c.ok, "message": c.msg}
		}
		return printJSON(map[string]any{"checks": out})
	}

	allOK := true
	for _, c := range checks {
		icon := "✓"
		if !c.ok {
			icon = "✗"
			allOK = false
		}
		fmt.Printf("  %s %s: %s\n", icon, c.name, c.msg)
	}
	fmt.Println()
	if allOK {
		fmt.Println("All checks passed.")
	} else {
		fmt.Println("Some checks failed.")
	}
	return nil
}

// ─── qr ───────────────────────────────────────────────────────────────────────

func runQR(args []string) error {
	fs := flag.NewFlagSet("qr", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.get("/status")
	if err != nil {
		return fmt.Errorf("status: %w", err)
	}

	pubkey := stringFieldAny(result, "pubkey")
	if pubkey == "" {
		return fmt.Errorf("could not retrieve agent pubkey from daemon")
	}

	// Print nostr: URI and a minimal block-char QR representation.
	nostrURI := "nostr:" + pubkey
	fmt.Printf("Agent pubkey: %s\n\n", pubkey)
	fmt.Printf("Nostr URI: %s\n\n", nostrURI)
	fmt.Println("(Install a QR-capable terminal or scan the URI with a Nostr client)")
	fmt.Println()
	printTerminalQR(nostrURI)
	return nil
}

// printTerminalQR renders a QR code to the terminal using Unicode half-block
// characters (▀▄█ ) for a compact, scannable representation.
func printTerminalQR(data string) {
	qr, err := qrcode.New(data, qrcode.Medium)
	if err != nil {
		// Fall back to plain text if QR encoding fails.
		fmt.Printf("(QR encode failed: %v)\n", err)
		fmt.Printf("URI: %s\n", data)
		return
	}
	bitmap := qr.Bitmap()
	rows := len(bitmap)
	cols := 0
	if rows > 0 {
		cols = len(bitmap[0])
	}
	// Use pairs of rows to combine into Unicode half-block characters.
	// ▀ = top set, ▄ = bottom set, █ = both set, " " = neither.
	for y := 0; y < rows; y += 2 {
		for x := 0; x < cols; x++ {
			top := bitmap[y][x]
			bottom := false
			if y+1 < rows {
				bottom = bitmap[y+1][x]
			}
			switch {
			case top && bottom:
				fmt.Print("█")
			case top && !bottom:
				fmt.Print("▀")
			case !top && bottom:
				fmt.Print("▄")
			default:
				fmt.Print(" ")
			}
		}
		fmt.Println()
	}
}

// ─── completion ───────────────────────────────────────────────────────────────

func runCompletion(args []string) error {
	shell := "bash"
	if len(args) > 0 {
		shell = args[0]
	}
	switch shell {
	case "bash":
		fmt.Print(bashCompletion)
	case "zsh":
		fmt.Print(zshCompletion)
	case "fish":
		fmt.Print(fishCompletion)
	default:
		return fmt.Errorf("unknown shell %q; supported: bash, zsh, fish", shell)
	}
	return nil
}

const bashCompletion = `# metiq bash completion
# Add to ~/.bashrc:  source <(metiq completion bash)
_metiq_completions() {
	local commands="version status health logs models channels agents skills hooks secrets update security plugins config nodes sessions cron approvals doctor qr completion daemon gw plan bootstrap-check dm-send memory-search"
  local cur="${COMP_WORDS[COMP_CWORD]}"
  COMPREPLY=($(compgen -W "${commands}" -- "${cur}"))
}
complete -F _metiq_completions metiq
`

const zshCompletion = `# metiq zsh completion
# Add to ~/.zshrc:  source <(metiq completion zsh)
_metiq() {
  local commands=(
    'version:show version'
    'status:show daemon status'
    'health:health check'
    'logs:stream logs'
    'models:model management'
    'channels:channel management'
    'agents:agent management'
    'skills:skill management'
    'hooks:hook management'
    'secrets:secret management'
    'update:update metiq'
    'security:security audit'
    'plugins:plugin management'
    'config:config management'
    'nodes:remote node management'
    'sessions:session management'
    'cron:scheduled task management'
    'approvals:exec approval management'
    'doctor:system health diagnostics'
    'qr:display agent QR code'
    'completion:generate shell completions'
	'daemon:daemon lifecycle management'
	'gw:gateway method passthrough'
  )
  _describe 'commands' commands
}
compdef _metiq metiq
`

const fishCompletion = `# metiq fish completion
# Add to ~/.config/fish/completions/metiq.fish or: metiq completion fish | source
for cmd in version status health logs models channels agents skills hooks secrets update security plugins config nodes sessions cron approvals doctor qr completion daemon gw
  complete -c metiq -f -n '__fish_use_subcommand' -a $cmd
end
`

// floatFieldAny is like floatField but operates on map[string]any.
func floatFieldAny(m map[string]any, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

// ─── daemon ───────────────────────────────────────────────────────────────────

// defaultPIDFile returns ~/.metiq/metiqd.pid.
func defaultPIDFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".metiq", "metiqd.pid")
}

// defaultDaemonLog returns ~/.metiq/metiqd.log.
func defaultDaemonLog() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".metiq", "metiqd.log")
}

// resolveDaemonBin returns the path to the metiqd binary.
func resolveDaemonBin(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	self, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(self), "metiqd")
		if runtime.GOOS == "windows" {
			candidate += ".exe"
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return exec.LookPath("metiqd")
}

// readPID reads and parses the PID from a pid file.  Returns 0 and no error if
// the file does not exist.
func readPID(pidFile string) (int, error) {
	raw, err := os.ReadFile(pidFile)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, fmt.Errorf("invalid pid file %s: %w", pidFile, err)
	}
	return pid, nil
}

// pidAlive returns true if the process with pid is running and reachable.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds; we need to send signal 0.
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// processCommandLine returns the process command line for pid via `ps`.
func processCommandLine(pid int) (string, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func looksLikeMetiqdCommand(cmdline string) bool {
	cmdline = strings.TrimSpace(cmdline)
	if cmdline == "" {
		return false
	}
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return false
	}
	procPath := strings.ReplaceAll(fields[0], "\\", "/")
	exe := strings.ToLower(filepath.Base(procPath))
	return exe == "metiqd" || exe == "metiqd.exe"
}

// processLooksLikeMetiqd performs strict identity validation for daemon PID
// files to avoid signaling unrelated recycled PIDs.
func processLooksLikeMetiqd(pid int) (bool, string, error) {
	cmdline, err := processCommandLine(pid)
	if err != nil {
		return false, "", err
	}
	if cmdline == "" {
		return false, "", nil
	}
	if looksLikeMetiqdCommand(cmdline) {
		return true, cmdline, nil
	}
	return false, cmdline, nil
}

func runDaemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var pidFile, logFile, bin, bootstrapPath, adminAddr, adminToken string
	fs.StringVar(&pidFile, "pid-file", "", "PID file path (default: ~/.metiq/metiqd.pid)")
	fs.StringVar(&logFile, "log-file", "", "log file path for start (default: ~/.metiq/metiqd.log)")
	fs.StringVar(&bin, "bin", "", "path to metiqd binary (default: sibling or PATH)")
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path forwarded to metiqd")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (for status check)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	if err := fs.Parse(args); err != nil {
		return err
	}

	sub := fs.Args()
	if len(sub) == 0 {
		fmt.Fprintf(os.Stderr, "daemon subcommands: start, stop, restart, status\n")
		return fmt.Errorf("subcommand required")
	}

	if pidFile == "" {
		pidFile = defaultPIDFile()
	}
	if logFile == "" {
		logFile = defaultDaemonLog()
	}

	switch sub[0] {
	case "start":
		return daemonStart(bin, pidFile, logFile, bootstrapPath, sub[1:])
	case "stop":
		return daemonStop(pidFile)
	case "restart":
		_ = daemonStop(pidFile) // ignore error: may already be down
		time.Sleep(500 * time.Millisecond)
		return daemonStart(bin, pidFile, logFile, bootstrapPath, sub[1:])
	case "status":
		return daemonStatus(pidFile, adminAddr, adminToken, bootstrapPath)
	default:
		return fmt.Errorf("unknown daemon subcommand %q; use start|stop|restart|status", sub[0])
	}
}

func daemonStart(bin, pidFile, logFile, bootstrapPath string, extraArgs []string) error {
	// Check if already running.
	pid, err := readPID(pidFile)
	if err != nil {
		return err
	}
	if pid > 0 && pidAlive(pid) {
		isDaemon, cmdline, idErr := processLooksLikeMetiqd(pid)
		if idErr != nil {
			return fmt.Errorf("daemon pid %d is alive but identity check failed: %w", pid, idErr)
		}
		if isDaemon {
			return fmt.Errorf("daemon already running (pid=%d, pid-file=%s)", pid, pidFile)
		}
		return fmt.Errorf("pid file %s points to non-metiqd process pid=%d (%q); remove stale pid file manually", pidFile, pid, cmdline)
	}

	metiqd, err := resolveDaemonBin(bin)
	if err != nil {
		return fmt.Errorf("cannot find metiqd binary: %w\nSet --bin or ensure metiqd is on PATH", err)
	}

	// Ensure log dir exists.
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	lf, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", logFile, err)
	}
	defer lf.Close()

	// Build args for metiqd.
	cmdArgs := []string{"--pid-file", pidFile}
	if bootstrapPath != "" {
		cmdArgs = append(cmdArgs, "--bootstrap", bootstrapPath)
	}
	cmdArgs = append(cmdArgs, extraArgs...)

	cmd := exec.Command(metiqd, cmdArgs...)
	cmd.Stdout = lf
	cmd.Stderr = lf
	// Detach from this process group so the child survives our exit.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start metiqd: %w", err)
	}

	fmt.Printf("daemon started  pid=%d  log=%s\n", cmd.Process.Pid, logFile)
	return nil
}

func daemonStop(pidFile string) error {
	pid, err := readPID(pidFile)
	if err != nil {
		return err
	}
	if pid == 0 {
		return fmt.Errorf("no pid file found at %s — daemon may not be running", pidFile)
	}
	if !pidAlive(pid) {
		fmt.Printf("daemon not running (stale pid=%d); removing pid file\n", pid)
		_ = os.Remove(pidFile)
		return nil
	}
	isDaemon, cmdline, err := processLooksLikeMetiqd(pid)
	if err != nil {
		return fmt.Errorf("cannot validate process identity for pid %d: %w", pid, err)
	}
	if !isDaemon {
		return fmt.Errorf("refusing to signal pid %d from %s: process is not metiqd (%q)", pid, pidFile, cmdline)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM to pid %d: %w", pid, err)
	}
	// Wait up to 10 s for the process to exit.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		if !pidAlive(pid) {
			fmt.Printf("daemon stopped  pid=%d\n", pid)
			return nil
		}
	}
	// Force kill if still alive.
	_ = proc.Signal(syscall.SIGKILL)
	fmt.Printf("daemon killed   pid=%d (did not stop within 10s)\n", pid)
	return nil
}

func daemonStatus(pidFile, adminAddr, adminToken, bootstrapPath string) error {
	pid, err := readPID(pidFile)
	if err != nil {
		return err
	}

	if pid == 0 {
		fmt.Printf("● metiqd  status=stopped  (no pid file at %s)\n", pidFile)
		return nil
	}
	if !pidAlive(pid) {
		fmt.Printf("● metiqd  status=stopped  (stale pid=%d, pid-file=%s)\n", pid, pidFile)
		return nil
	}
	isDaemon, cmdline, idErr := processLooksLikeMetiqd(pid)
	if idErr != nil {
		fmt.Printf("● metiqd  status=unknown  pid=%d  (identity check failed: %v)\n", pid, idErr)
		return nil
	}
	if !isDaemon {
		fmt.Printf("● metiqd  status=unknown  pid=%d  (pid file points to non-metiqd process: %q)\n", pid, cmdline)
		return nil
	}
	fmt.Printf("● metiqd  status=running  pid=%d\n", pid)

	// Optionally query the admin API for richer info.
	if adminAddr != "" || bootstrapPath != "" {
		cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
		if err != nil {
			fmt.Printf("  (could not reach admin API: %v)\n", err)
			return nil
		}
		result, err := cl.get("/status")
		if err != nil {
			fmt.Printf("  (admin API unreachable: %v)\n", err)
			return nil
		}
		uptime := floatField(result, "uptime_seconds")
		ver := stringField(result, "version")
		pubkey := stringField(result, "pubkey")
		if len(pubkey) > 16 {
			pubkey = pubkey[:16] + "..."
		}
		fmt.Printf("  version=%s  uptime=%.0fs  pubkey=%s\n", ver, uptime, pubkey)
	}
	return nil
}

// ─── gw (gateway passthrough) ─────────────────────────────────────────────────

func runGW(args []string) error {
	fs := flag.NewFlagSet("gw", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var adminAddr, adminToken, bootstrapPath string
	var transport, controlTargetPubKey, controlSignerURL string
	var timeoutSec int
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&transport, "transport", "auto", "gateway transport: auto, http, or nostr")
	fs.StringVar(&controlTargetPubKey, "control-target-pubkey", "", "target daemon pubkey for Nostr control RPC")
	fs.StringVar(&controlSignerURL, "control-signer-url", "", "caller signer override for Nostr control RPC (URL, env://, file://, bunker://, or direct key material)")
	fs.IntVar(&timeoutSec, "timeout", 30, "request timeout seconds")
	fs.BoolVar(&jsonOut, "json", true, "output raw JSON (default true)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	positional := fs.Args()
	if len(positional) == 0 {
		return fmt.Errorf("usage: metiq gw <method> [json-params]")
	}
	method := positional[0]

	// Collect JSON params: remaining positional args joined, or '{}' if none.
	var rawParams json.RawMessage
	if len(positional) > 1 {
		paramStr := strings.Join(positional[1:], " ")
		// Accept bare key=value pairs as a convenience shorthand.
		if !strings.HasPrefix(strings.TrimSpace(paramStr), "{") {
			// Try to build an object from key=value pairs.
			pairs := strings.Fields(paramStr)
			obj := map[string]string{}
			for _, p := range pairs {
				kv := strings.SplitN(p, "=", 2)
				if len(kv) == 2 {
					obj[kv[0]] = kv[1]
				}
			}
			b, _ := json.Marshal(obj)
			rawParams = b
		} else {
			rawParams = json.RawMessage(paramStr)
		}
	} else {
		rawParams = json.RawMessage("{}")
	}

	cl, err := resolveGWClientFn(transport, adminAddr, adminToken, bootstrapPath, controlTargetPubKey, controlSignerURL, time.Duration(timeoutSec)*time.Second)
	if err != nil {
		return err
	}
	if closer, ok := cl.(gatewayCloser); ok {
		defer closer.Close()
	}

	// Use cl.call; json.RawMessage marshals as-is so params stay intact.
	result, err := cl.call(method, rawParams)
	if err != nil {
		return fmt.Errorf("gw %s: %w", method, err)
	}

	if jsonOut {
		return printJSON(result)
	}
	fmt.Printf("%v\n", result)
	return nil
}

// ─── keygen ───────────────────────────────────────────────────────────────────

// runKeygen generates a fresh Nostr keypair (nsec + npub) and prints them.
// It does not persist anything; the operator adds the nsec to their config or
// environment and treats the npub as the public identity.
func runKeygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Generate 32 random bytes for the secret key.
	var skBytes [32]byte
	if _, err := rand.Read(skBytes[:]); err != nil {
		return fmt.Errorf("keygen: failed to generate random key: %w", err)
	}

	// Derive public key using secp256k1 scalar multiplication.
	// We use the nostr library's hex encoding for nsec/npub bech32.
	skHex := hex.EncodeToString(skBytes[:])

	// Use metiq's config package to produce bech32 keys.
	nsec, npub, err := config.KeypairFromHex(skHex)
	if err != nil {
		return fmt.Errorf("keygen: %w", err)
	}

	if jsonOut {
		out, _ := json.MarshalIndent(map[string]string{
			"nsec": nsec,
			"npub": npub,
			"hex":  skHex,
		}, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	fmt.Printf("nsec: %s\n", nsec)
	fmt.Printf("npub: %s\n", npub)
	fmt.Printf("\n")
	fmt.Printf("Add to your environment or bootstrap config:\n")
	fmt.Printf("  NOSTR_NSEC=%s\n", nsec)
	fmt.Printf("\nKeep the nsec secret — it is your private signing key.\n")
	return nil
}
