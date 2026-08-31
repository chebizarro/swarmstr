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
	// Run is retained for tests and tiny legacy commands. New registry entries
	// should prefer Load so startup records metadata only and defers handler setup
	// until dispatch.
	Run    func([]string) error
	Load   func() (func([]string) error, error)
	Hidden bool
	Legacy bool
}

type commandRegistry struct {
	commands []*cliCommand
	byName   map[string]*cliCommand
}

func newCommandRegistry(bootstrapPath string) *commandRegistry {
	r := &commandRegistry{byName: map[string]*cliCommand{}}
	add := func(cmd cliCommand) { r.add(cmd) }
	addLazy := func(cmd cliCommand, run func([]string) error) {
		cmd.Load = func() (func([]string) error, error) { return run, nil }
		add(cmd)
	}
	addLazyBootstrap := func(cmd cliCommand, factory func(string) func([]string) error) {
		cmd.Load = func() (func([]string) error, error) { return factory(bootstrapPath), nil }
		add(cmd)
	}

	addLazy(cliCommand{Name: "help", Summary: "show grouped help or command details", Group: "Other"}, runHelp)
	addLazy(cliCommand{Name: "version", Aliases: []string{"--version", "-version"}, Summary: "print version", Group: "Other"}, runVersion)
	addLazy(cliCommand{Name: "status", Summary: "show daemon status (pubkey, uptime, relays)", Group: "Daemon Status"}, runStatus)
	addLazy(cliCommand{Name: "health", Summary: "ping daemon health endpoint", Group: "Daemon Status"}, runHealth)
	addLazy(cliCommand{Name: "logs", Summary: "tail recent daemon log lines (--lines N)", Group: "Daemon Status"}, runLogs)
	addLazy(cliCommand{Name: "observe", Summary: "inspect structured runtime events/logs (--event, --wait)", Group: "Daemon Status"}, runObserve)

	addLazy(cliCommand{Name: "agents", Summary: "agent management", Group: "Agent Management"}, runAgents)
	addLazy(cliCommand{Name: "models", Summary: "model management", Group: "Agent Management"}, runModels)

	addLazy(cliCommand{Name: "channels", Summary: "list configured channels and their status", Group: "Channels & Skills"}, runChannels)
	addLazy(cliCommand{Name: "skills", Summary: "skill management", Group: "Channels & Skills", Details: []string{
		"skills list        list installed skills",
		"skills status      detailed skills status",
		"skills check       check skill readiness",
		"skills info <id>   show one skill in detail",
		"skills install     install a skill option",
		"skills enable <id> enable a skill",
		"skills disable <id> disable a skill",
		"skills lint <path>  lint skill manifests for CI/editor use",
	}}, runSkills)
	addLazy(cliCommand{Name: "hooks", Summary: "list installed hooks", Group: "Channels & Skills"}, runHooks)

	addLazy(cliCommand{Name: "config", Summary: "config management", Group: "Config"}, runConfig)
	addLazy(cliCommand{Name: "lists", Aliases: []string{"list"}, Summary: "runtime list docs", Group: "Config"}, runLists)
	addLazy(cliCommand{Name: "setup", Summary: "interactive first-run setup", Group: "Config"}, runInteractiveSetup)
	addLazy(cliCommand{Name: "onboard", Summary: "guided onboarding checklist", Group: "Config"}, runInteractiveSetup)
	addLazy(cliCommand{Name: "configure", Summary: "guided configuration flow", Group: "Config"}, runInteractiveSetup)

	addLazy(cliCommand{Name: "secrets", Summary: "secret management", Group: "Secrets"}, runSecrets)
	addLazy(cliCommand{Name: "mcp", Summary: "MCP management", Group: "Secrets"}, runMCP)

	addLazy(cliCommand{Name: "plugins", Summary: "plugin management", Group: "Plugins", Details: []string{
		"plugins list                 list installed plugins",
		"plugins install              install from npm/git/url/archive/path",
		"plugins build <path>         validate, build, and package a plugin",
		"plugins validate <manifest>  validate a plugin manifest",
		"plugins enable|disable <id>  toggle plugin config entry",
		"plugins doctor               run plugin diagnostics",
	}}, runPlugins)
	addLazy(cliCommand{Name: "tasks", Aliases: []string{"task"}, Summary: "task management", Group: "Tasks"}, runTasks)
	addLazy(cliCommand{Name: "trajectory", Summary: "session trajectory export and cleanup", Group: "Tasks"}, runTrajectory)
	addLazy(cliCommand{Name: "qa", Summary: "run deterministic QA scenario packs", Group: "Tasks"}, runQA)
	addLazy(cliCommand{Name: "daemon", Summary: "daemon lifecycle management", Group: "Daemon Lifecycle"}, runDaemon)
	addLazy(cliCommand{Name: "gw", Aliases: []string{"gateway"}, Summary: "gateway method passthrough", Group: "Gateway Passthrough"}, runGW)
	addLazy(cliCommand{Name: "acp", Summary: "run and inspect ACP-backed coding agents", Group: "Gateway Passthrough", Details: []string{
		"acp dispatch --target-pubkey <pubkey> --instructions <text>",
		"acp pipeline '{\"steps\":[...]}'",
		"acp status",
	}}, runACP)
	addLazy(cliCommand{Name: "commitments", Summary: "list and manage inferred follow-up commitments", Group: "Gateway Passthrough", Details: []string{
		"commitments list [--all] [--agent <id>] [--status <status>]",
		"commitments add --text <text> [--agent <id>] [--due-at <time>]",
		"commitments status",
	}}, runCommitments)
	addLazy(cliCommand{Name: "sandbox", Summary: "manage sandbox containers for agent isolation", Group: "Gateway Passthrough", Details: []string{
		"sandbox run <command> [args...]",
		"sandbox status",
	}}, runSandbox)
	addLazy(cliCommand{Name: "message", Summary: "send messages through the gateway", Group: "Gateway Passthrough", Details: []string{
		"message send --to <target> --text <message>",
	}}, runMessage)
	addLazy(cliCommand{Name: "send", Summary: "send a message through the gateway", Group: "Gateway Passthrough"}, runSend)
	addLazy(cliCommand{Name: "transcripts", Summary: "inspect and export stored transcripts", Group: "Gateway Passthrough", Details: []string{
		"transcripts list [--limit N]",
		"transcripts export <session-id> [--output path]",
	}}, runTranscripts)
	addLazy(cliCommand{Name: "system", Summary: "system status and info aggregate", Group: "Gateway Passthrough", Details: []string{
		"system status",
		"system info",
	}}, runSystem)
	addLazy(cliCommand{Name: "migrate", Summary: "migrate OpenClaw agent to Metiq", Group: "Migration"}, runMigrate)
	addLazy(cliCommand{Name: "memory", Summary: "memory management", Group: "Memory"}, runMemory)

	addLazy(cliCommand{Name: "nodes", Aliases: []string{"node"}, Summary: "remote node management", Group: "Other"}, runNodes)
	addLazy(cliCommand{Name: "sessions", Aliases: []string{"session"}, Summary: "session management", Group: "Other"}, runSessions)
	addLazy(cliCommand{Name: "cron", Aliases: []string{"automations"}, Summary: "scheduled task management", Group: "Other"}, runCron)
	addLazy(cliCommand{Name: "approvals", Aliases: []string{"approval", "exec-approvals"}, Summary: "exec approval management", Group: "Other"}, runApprovals)
	addLazy(cliCommand{Name: "exec-policy", Aliases: []string{"execpolicy"}, Summary: "exec approval policy management", Group: "Other", Details: []string{
		"exec-policy get              show live policy",
		"exec-policy doctor           diagnose invalid/conflicting/unreachable rules",
	}}, runExecPolicy)
	addLazy(cliCommand{Name: "backup", Summary: "create or restore local metiq backups", Group: "Other"}, runBackup)
	addLazy(cliCommand{Name: "diagnostics", Aliases: []string{"diag", "triage"}, Summary: "export support diagnostic bundle", Group: "Other"}, runDiagnostics)
	addLazy(cliCommand{Name: "doctor", Summary: "system health diagnostics", Group: "Other"}, runDoctor)
	addLazy(cliCommand{Name: "qr", Summary: "display agent QR code", Group: "Other"}, runQR)
	addLazy(cliCommand{Name: "completion", Summary: "generate shell completions", Group: "Other"}, runCompletion)
	addLazy(cliCommand{Name: "update", Summary: "check for daemon updates", Group: "Other"}, runUpdate)
	addLazy(cliCommand{Name: "security", Summary: "run local security posture checks", Group: "Other", Details: []string{
		"security audit              run local security audit",
		"security doctor            run policy conformance doctor",
	}}, runSecurity)
	addLazy(cliCommand{Name: "keygen", Summary: "generate keys", Group: "Other"}, runKeygen)

	addLazy(cliCommand{Name: "plan", Summary: "print port plan path", Group: "Other", Legacy: true}, func(_ []string) error { fmt.Println("docs/PORT_PLAN.md"); return nil })
	addLazy(cliCommand{Name: "init", Summary: "initialize metiq", Group: "Other", Legacy: true}, runInit)
	addLazyBootstrap(cliCommand{Name: "bootstrap-check", Summary: "validate bootstrap config", Group: "Other", Legacy: true}, func(path string) func([]string) error {
		return func(args []string) error { return runBootstrapCheck(path, args) }
	})
	addLazyBootstrap(cliCommand{Name: "dm-send", Summary: "send a NIP-17 DM (--to --text)", Group: "Other", Legacy: true}, func(path string) func([]string) error {
		return func(args []string) error { return runDMSend(path, args) }
	})
	addLazy(cliCommand{Name: "memory-search", Summary: "search local memory index (--q [--limit])", Group: "Other", Legacy: true}, runMemorySearch)
	addLazy(cliCommand{Name: "config-export", Summary: "export config", Group: "Other", Legacy: true}, runConfigExport)
	addLazy(cliCommand{Name: "config-import", Summary: "import config", Group: "Other", Legacy: true}, runConfigImport)
	addLazyBootstrap(cliCommand{Name: "plugin-publish", Summary: "publish plugin manifest", Group: "Other", Legacy: true}, func(path string) func([]string) error {
		return func(args []string) error { return runPluginPublish(path, args) }
	})
	addLazyBootstrap(cliCommand{Name: "plugin-search", Summary: "search Nostr plugin registry", Group: "Other", Legacy: true}, func(path string) func([]string) error {
		return func(args []string) error { return runPluginSearch(path, args) }
	})
	addLazyBootstrap(cliCommand{Name: "plugin-install", Summary: "install plugin from Nostr", Group: "Other", Legacy: true}, func(path string) func([]string) error {
		return func(args []string) error { return runPluginInstall(path, args) }
	})

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
	args = consumeLeadingGlobalFlags(args)
	if len(args) == 0 {
		return false, nil
	}
	cmd, ok := r.byName[args[0]]
	if !ok {
		return false, nil
	}
	if len(args) > 1 && (args[1] == "--help" || args[1] == "-h" || args[1] == "help") {
		printCommandHelp(cmd)
		return true, nil
	}
	cmdArgs := consumeCommandGlobalFlags(args[1:])
	applyCLIOutputMode()
	run, err := cmd.handler()
	if err != nil {
		return true, err
	}
	return true, run(cmdArgs)
}

func (c *cliCommand) handler() (func([]string) error, error) {
	if c.Run != nil {
		return c.Run, nil
	}
	if c.Load == nil {
		return nil, fmt.Errorf("command %q has no handler", c.Name)
	}
	run, err := c.Load()
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, fmt.Errorf("command %q loaded no handler", c.Name)
	}
	c.Run = run
	return c.Run, nil
}

func consumeLeadingGlobalFlags(args []string) []string {
	out := args[:0]
	beforeCommand := true
	for _, arg := range args {
		if beforeCommand {
			if handledCLIGlobalFlag(arg) {
				continue
			}
			beforeCommand = false
		}
		out = append(out, arg)
	}
	return out
}

func consumeCommandGlobalFlags(args []string) []string {
	out := args[:0]
	for _, arg := range args {
		if handledCLIGlobalFlag(arg) {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func handledCLIGlobalFlag(arg string) bool {
	switch {
	case arg == "--json":
		cliGlobalJSON = true
		return true
	case strings.HasPrefix(arg, "--json="):
		cliGlobalJSON = parseCLIFlagBool(strings.TrimPrefix(arg, "--json="), true)
		return true
	case arg == "--no-color":
		cliNoColor = true
		return true
	case strings.HasPrefix(arg, "--no-color="):
		cliNoColor = parseCLIFlagBool(strings.TrimPrefix(arg, "--no-color="), true)
		return true
	default:
		return false
	}
}

func parseCLIFlagBool(raw string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	case "0", "f", "false", "n", "no", "off":
		return false
	default:
		return fallback
	}
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

func (r *commandRegistry) lookup(name string) (*cliCommand, bool) {
	cmd, ok := r.byName[name]
	return cmd, ok
}

func (r *commandRegistry) suggestions(name string, limit int) []string {
	if limit <= 0 {
		limit = 3
	}
	type candidate struct {
		name string
		dist int
	}
	candidates := make([]candidate, 0, len(r.visibleCommands()))
	for _, cmd := range r.visibleCommands() {
		d := levenshtein(strings.ToLower(name), strings.ToLower(cmd.Name))
		if d <= 3 || strings.HasPrefix(cmd.Name, name) || strings.HasPrefix(name, cmd.Name) {
			candidates = append(candidates, candidate{name: cmd.Name, dist: d})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].dist == candidates[j].dist {
			return candidates[i].name < candidates[j].name
		}
		return candidates[i].dist < candidates[j].dist
	})
	out := make([]string, 0, limit)
	seen := map[string]bool{}
	for _, c := range candidates {
		if seen[c.name] {
			continue
		}
		seen[c.name] = true
		out = append(out, c.name)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return len(b)
	}
	if b == "" {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			cur[j] = minInt(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

func minInt(vals ...int) int {
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func runHelp(args []string) error {
	reg := currentRegistry()
	if len(args) == 0 {
		usage()
		return nil
	}
	cmd, ok := reg.lookup(args[0])
	if !ok {
		if suggestions := reg.suggestions(args[0], 3); len(suggestions) > 0 {
			return fmt.Errorf("unknown command %q; did you mean %s?", args[0], strings.Join(suggestions, ", "))
		}
		return fmt.Errorf("unknown command %q", args[0])
	}
	printCommandHelp(cmd)
	return nil
}

func printCommandHelp(cmd *cliCommand) {
	printListHeader(cmd.Name)
	printInfo("%s", cmd.Summary)
	if len(cmd.Aliases) > 0 {
		printMuted("Aliases: %s", strings.Join(cmd.Aliases, ", "))
	}
	if len(cmd.Details) > 0 {
		printBlankLine()
		printListHeader("Subcommands")
		for _, detail := range cmd.Details {
			printMuted("  %s", detail)
		}
	}
	printBlankLine()
	printMuted("Run %s for generated shell completions.", printCommand("metiq completion <bash|zsh|fish>"))
}

func runConfig(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("config subcommands: get, set, unset, patch, list, schema, validate, path, import, export, wizard")
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
	case "wizard", "setup", "configure":
		return runConfigWizard(args[1:])
	default:
		return fmt.Errorf("config subcommands: get, set, unset, patch, list, schema, validate, path, import, export, wizard")
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
