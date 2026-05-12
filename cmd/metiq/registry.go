package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"metiq/internal/config"
)

type cliCommand struct {
	Name    string
	Aliases []string
	Summary string
	Group   string
	Details []string
	Run     func([]string) error
	Hidden  bool
	Legacy  bool
}

type commandRegistry struct {
	commands []*cliCommand
	byName   map[string]*cliCommand
}

func newCommandRegistry(bootstrapPath string) *commandRegistry {
	r := &commandRegistry{byName: map[string]*cliCommand{}}
	add := func(cmd cliCommand) { r.add(cmd) }

	add(cliCommand{Name: "version", Aliases: []string{"--version", "-version"}, Summary: "print version", Group: "Other", Run: runVersion})
	add(cliCommand{Name: "status", Summary: "show daemon status (pubkey, uptime, relays)", Group: "Daemon Status", Run: runStatus})
	add(cliCommand{Name: "health", Summary: "ping daemon health endpoint", Group: "Daemon Status", Run: runHealth})
	add(cliCommand{Name: "logs", Summary: "tail recent daemon log lines (--lines N)", Group: "Daemon Status", Run: runLogs})
	add(cliCommand{Name: "observe", Summary: "inspect structured runtime events/logs (--event, --wait)", Group: "Daemon Status", Run: runObserve})

	add(cliCommand{Name: "agents", Summary: "agent management", Group: "Agent Management", Run: runAgents})
	add(cliCommand{Name: "models", Summary: "model management", Group: "Agent Management", Run: runModels})

	add(cliCommand{Name: "channels", Summary: "list configured channels and their status", Group: "Channels & Skills", Run: runChannels})
	add(cliCommand{Name: "skills", Summary: "skill management", Group: "Channels & Skills", Details: []string{
		"skills list        list installed skills",
		"skills status      detailed skills status",
		"skills check       check skill readiness",
		"skills info <id>   show one skill in detail",
		"skills install     install a skill option",
		"skills enable <id> enable a skill",
		"skills disable <id> disable a skill",
	}, Run: runSkills})
	add(cliCommand{Name: "hooks", Summary: "list installed hooks", Group: "Channels & Skills", Run: runHooks})

	add(cliCommand{Name: "config", Summary: "config management", Group: "Config", Run: runConfig})
	add(cliCommand{Name: "lists", Aliases: []string{"list"}, Summary: "runtime list docs", Group: "Config", Run: runLists})
	add(cliCommand{Name: "setup", Summary: "interactive first-run setup", Group: "Config", Run: runInteractiveSetup})
	add(cliCommand{Name: "onboard", Summary: "guided onboarding checklist", Group: "Config", Run: runInteractiveSetup})
	add(cliCommand{Name: "configure", Summary: "guided configuration flow", Group: "Config", Run: runInteractiveSetup})

	add(cliCommand{Name: "secrets", Summary: "secret management", Group: "Secrets", Run: runSecrets})
	add(cliCommand{Name: "mcp", Summary: "MCP management", Group: "Secrets", Run: runMCP})

	add(cliCommand{Name: "plugins", Summary: "plugin management", Group: "Plugins", Run: runPlugins})
	add(cliCommand{Name: "tasks", Aliases: []string{"task"}, Summary: "task management", Group: "Tasks", Run: runTasks})
	add(cliCommand{Name: "daemon", Summary: "daemon lifecycle management", Group: "Daemon Lifecycle", Run: runDaemon})
	add(cliCommand{Name: "gw", Summary: "gateway method passthrough", Group: "Gateway Passthrough", Run: runGW})
	add(cliCommand{Name: "migrate", Summary: "migrate OpenClaw agent to Metiq", Group: "Migration", Run: runMigrate})
	add(cliCommand{Name: "memory", Summary: "memory management", Group: "Memory", Run: runMemory})

	add(cliCommand{Name: "nodes", Aliases: []string{"node"}, Summary: "remote node management", Group: "Other", Run: runNodes})
	add(cliCommand{Name: "sessions", Aliases: []string{"session"}, Summary: "session management", Group: "Other", Run: runSessions})
	add(cliCommand{Name: "cron", Summary: "scheduled task management", Group: "Other", Run: runCron})
	add(cliCommand{Name: "approvals", Aliases: []string{"approval"}, Summary: "exec approval management", Group: "Other", Run: runApprovals})
	add(cliCommand{Name: "doctor", Summary: "system health diagnostics", Group: "Other", Run: runDoctor})
	add(cliCommand{Name: "qr", Summary: "display agent QR code", Group: "Other", Run: runQR})
	add(cliCommand{Name: "completion", Summary: "generate shell completions", Group: "Other", Run: runCompletion})
	add(cliCommand{Name: "update", Summary: "check for daemon updates", Group: "Other", Run: runUpdate})
	add(cliCommand{Name: "security", Summary: "run local security posture checks", Group: "Other", Run: runSecurity})
	add(cliCommand{Name: "keygen", Summary: "generate keys", Group: "Other", Run: runKeygen})

	add(cliCommand{Name: "plan", Summary: "print port plan path", Group: "Other", Run: func(_ []string) error { fmt.Println("docs/PORT_PLAN.md"); return nil }, Legacy: true})
	add(cliCommand{Name: "init", Summary: "initialize metiq", Group: "Other", Run: runInit, Legacy: true})
	add(cliCommand{Name: "bootstrap-check", Summary: "validate bootstrap config", Group: "Other", Run: func(args []string) error { return runBootstrapCheck(bootstrapPath, args) }, Legacy: true})
	add(cliCommand{Name: "dm-send", Summary: "send a NIP-17 DM (--to --text)", Group: "Other", Run: func(args []string) error { return runDMSend(bootstrapPath, args) }, Legacy: true})
	add(cliCommand{Name: "memory-search", Summary: "search local memory index (--q [--limit])", Group: "Other", Run: runMemorySearch, Legacy: true})
	add(cliCommand{Name: "config-export", Summary: "export config", Group: "Other", Run: runConfigExport, Legacy: true})
	add(cliCommand{Name: "config-import", Summary: "import config", Group: "Other", Run: runConfigImport, Legacy: true})
	add(cliCommand{Name: "plugin-publish", Summary: "publish plugin manifest", Group: "Other", Run: func(args []string) error { return runPluginPublish(bootstrapPath, args) }, Legacy: true})
	add(cliCommand{Name: "plugin-search", Summary: "search Nostr plugin registry", Group: "Other", Run: func(args []string) error { return runPluginSearch(bootstrapPath, args) }, Legacy: true})
	add(cliCommand{Name: "plugin-install", Summary: "install plugin from Nostr", Group: "Other", Run: func(args []string) error { return runPluginInstall(bootstrapPath, args) }, Legacy: true})

	return r
}

func (r *commandRegistry) add(cmd cliCommand) {
	c := cmd
	r.commands = append(r.commands, &c)
	r.byName[c.Name] = &c
	for _, alias := range c.Aliases {
		r.byName[alias] = &c
	}
}

func (r *commandRegistry) dispatch(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	cmd, ok := r.byName[args[0]]
	if !ok {
		return false, nil
	}
	return true, cmd.Run(args[1:])
}

func (r *commandRegistry) visibleCommands() []*cliCommand {
	out := make([]*cliCommand, 0, len(r.commands))
	for _, cmd := range r.commands {
		if !cmd.Hidden {
			out = append(out, cmd)
		}
	}
	return out
}

func (r *commandRegistry) commandNames() []string {
	names := make([]string, 0, len(r.commands))
	for _, cmd := range r.visibleCommands() {
		names = append(names, cmd.Name)
	}
	sort.Strings(names)
	return names
}

func (r *commandRegistry) commandsByGroup() map[string][]*cliCommand {
	groups := map[string][]*cliCommand{}
	for _, cmd := range r.visibleCommands() {
		groups[cmd.Group] = append(groups[cmd.Group], cmd)
	}
	for group := range groups {
		sort.Slice(groups[group], func(i, j int) bool { return groups[group][i].Name < groups[group][j].Name })
	}
	return groups
}

func currentRegistry() *commandRegistry { return newCommandRegistry("") }

func runConfig(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("config subcommands: get, set, unset, patch, list, schema, validate, path, import, export")
	}
	switch args[0] {
	case "get":
		return runConfigGet(args[1:])
	case "set":
		return runConfigSet(args[1:])
	case "unset":
		return runConfigUnset(args[1:])
	case "patch":
		return runConfigPatch(args[1:])
	case "list":
		return runConfigList(args[1:])
	case "schema":
		return runConfigSchema(args[1:])
	case "validate":
		return runConfigValidate(args[1:])
	case "path":
		return runConfigPath(args[1:])
	case "import":
		return runConfigImport(args[1:])
	case "export":
		return runConfigExport(args[1:])
	default:
		return fmt.Errorf("config subcommands: get, set, unset, patch, list, schema, validate, path, import, export")
	}
}

func runBootstrapCheck(bootstrapPath string, _ []string) error {
	cfg, err := config.LoadBootstrap(bootstrapPath)
	if err != nil {
		return fmt.Errorf("bootstrap invalid: %w", err)
	}
	fmt.Printf("bootstrap ok: relays=%d state_kind=%d transcript_kind=%d\n",
		len(cfg.Relays), cfg.EffectiveStateKind(), cfg.EffectiveTranscriptKind())
	return nil
}

func runInteractiveSetup(args []string) error {
	in := os.Stdin
	out := os.Stdout
	path := ""
	if len(args) >= 2 && args[0] == "--path" {
		path = args[1]
	}
	return interactiveSetup(in, out, path)
}

func interactiveSetup(in io.Reader, out io.Writer, path string) error {
	reader := bufio.NewReader(in)
	fmt.Fprintln(out, "Metiq setup")
	if strings.TrimSpace(path) == "" {
		def, err := config.DefaultConfigPath()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Config path [%s]: ", def)
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		path = strings.TrimSpace(line)
		if path == "" {
			path = def
		}
	}
	fmt.Fprintf(out, "Using config path: %s\n", path)
	fmt.Fprintln(out, "Next steps:")
	fmt.Fprintln(out, "  1. Run `metiq config validate --path <path>` to verify configuration.")
	fmt.Fprintln(out, "  2. Run `metiq daemon start --bootstrap <path>` when ready.")
	return nil
}
