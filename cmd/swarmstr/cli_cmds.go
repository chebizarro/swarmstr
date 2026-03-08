package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"swarmstr/internal/config"
	"swarmstr/internal/security"
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

	fmt.Printf("● swarmstrd running\n")
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
	fmt.Printf("swarmstr %s\n", version)
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
		return fmt.Errorf("usage: swarmstr models set <model-id> [--agent <id>]")
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
		status := stringField(ch, "status")
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
		return fmt.Errorf("usage: swarmstr secrets get <key>")
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
		return fmt.Errorf("usage: swarmstr secrets set <key> <value>")
	}
	key := fs.Arg(0)
	value := fs.Arg(1)

	_ = value
	return fmt.Errorf("secrets set is not supported by the daemon API; set %q in your environment or .env and run `swarmstr secrets list` (reload)", key)
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
		fmt.Printf("update available — run: curl -fsSL https://raw.githubusercontent.com/swarmstr/swarmstr/main/scripts/install.sh | bash\n")
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

// runNodesList lists known remote swarmstr nodes via the daemon's node.list method.
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
		return fmt.Errorf("usage: swarmstr nodes add <npub|hex-pubkey> [--name <label>]")
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
		return fmt.Errorf("usage: swarmstr nodes status <npub|hex-pubkey>")
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

// runNodesSend sends a DM to a remote swarmstr node.
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
		return fmt.Errorf("usage: swarmstr nodes send <npub|hex-pubkey> <message>")
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
	default:
		return fmt.Errorf("unknown nodes sub-command %q (list|add|status|send)", args[0])
	}
}

// stringFieldAny is like stringField but operates on map[string]any.
func stringFieldAny(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
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
	default:
		return fmt.Errorf("unknown sessions sub-command %q (list|get|export|delete|reset)", args[0])
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
		return fmt.Errorf("usage: swarmstr sessions get <session-id>")
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
		return fmt.Errorf("usage: swarmstr sessions export <session-id> [--output path]")
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
		return fmt.Errorf("usage: swarmstr sessions delete <session-id>")
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
		return fmt.Errorf("usage: swarmstr sessions reset <session-id>")
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
		return fmt.Errorf("usage: swarmstr approvals %s <approval-id>", decision)
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
	var adminAddr, adminToken, bootstrapPath, id, schedule, message, agentID, description string
	var enabled bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.StringVar(&id, "id", "", "cron job ID (required)")
	fs.StringVar(&schedule, "schedule", "", "cron schedule expression (required)")
	fs.StringVar(&message, "message", "", "message to send on trigger (required)")
	fs.StringVar(&agentID, "agent", "main", "agent ID to target")
	fs.StringVar(&description, "description", "", "human-readable description")
	fs.BoolVar(&enabled, "enabled", true, "enable job immediately")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if id == "" || schedule == "" || message == "" {
		return fmt.Errorf("--id, --schedule, and --message are required")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("cron.add", map[string]any{
		"id":          id,
		"schedule":    schedule,
		"message":     message,
		"agent_id":    agentID,
		"description": description,
		"enabled":     enabled,
	})
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
		return fmt.Errorf("usage: swarmstr cron remove <job-id>")
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
		return fmt.Errorf("usage: swarmstr cron run <job-id>")
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
			bootstrapPath = home + "/.config/swarmstr/bootstrap.json"
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
			docs := floatFieldAny(result, "doc_count")
			checks = append(checks, check{"memory index", true, fmt.Sprintf("%.0f docs", docs)})
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
	printASCIIQR(nostrURI)
	return nil
}

// printASCIIQR prints a simple terminal-friendly hint. A real QR implementation
// would use a QR encoding library; this is a placeholder that shows the URI
// prominently for copy/paste into QR generator tools.
func printASCIIQR(data string) {
	border := "████████████████████"
	fmt.Println(border)
	fmt.Println("██  Scan with Nostr  ██")
	fmt.Printf("██  %s\n", data[:clampLen(data, 30)])
	if len(data) > 30 {
		fmt.Printf("██  %s\n", data[30:clampLen(data, 60)])
	}
	if len(data) > 60 {
		fmt.Printf("██  %s\n", data[60:])
	}
	fmt.Println(border)
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

const bashCompletion = `# swarmstr bash completion
# Add to ~/.bashrc:  source <(swarmstr completion bash)
_swarmstr_completions() {
  local commands="version status health logs models channels agents skills hooks secrets update security plugins config nodes sessions cron approvals doctor qr completion plan bootstrap-check dm-send memory-search"
  local cur="${COMP_WORDS[COMP_CWORD]}"
  COMPREPLY=($(compgen -W "${commands}" -- "${cur}"))
}
complete -F _swarmstr_completions swarmstr
`

const zshCompletion = `# swarmstr zsh completion
# Add to ~/.zshrc:  source <(swarmstr completion zsh)
_swarmstr() {
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
    'update:update swarmstr'
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
  )
  _describe 'commands' commands
}
compdef _swarmstr swarmstr
`

const fishCompletion = `# swarmstr fish completion
# Add to ~/.config/fish/completions/swarmstr.fish or: swarmstr completion fish | source
for cmd in version status health logs models channels agents skills hooks secrets update security plugins config nodes sessions cron approvals doctor qr completion
  complete -c swarmstr -f -n '__fish_use_subcommand' -a $cmd
end
`

// floatFieldAny is like floatField but operates on map[string]any.
func floatFieldAny(m map[string]any, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

func clampLen(s string, n int) int {
	if len(s) < n {
		return len(s)
	}
	return n
}
