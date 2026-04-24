package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

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
	case "info", "show":
		return runPluginsInfo(args[1:])
	case "capabilities", "caps":
		return runPluginsCapabilities(args[1:])
	case "install":
		return runPluginInstall("", args[1:])
	case "search":
		return runPluginSearch("", args[1:])
	case "publish":
		return runPluginPublish("", args[1:])
	default:
		fmt.Fprintf(os.Stderr, "plugins subcommands: list, info, capabilities, install, search, publish\n")
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func runPluginsInfo(args []string) error {
	fs := flag.NewFlagSet("plugins info", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq plugins info <plugin-id>")
	}
	pluginID := fs.Arg(0)

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("plugins.info", map[string]any{"id": pluginID})
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(result)
	}

	plugin, _ := result["plugin"].(map[string]any)
	if len(plugin) == 0 {
		return fmt.Errorf("plugin %q not found", pluginID)
	}

	fmt.Printf("Plugin: %s\n", stringField(plugin, "id"))
	fmt.Printf("  Version:     %s\n", stringField(plugin, "version"))
	fmt.Printf("  Runtime:     %s\n", stringField(plugin, "runtime"))
	if desc := stringField(plugin, "description"); desc != "" {
		fmt.Printf("  Description: %s\n", desc)
	}
	if author, ok := plugin["author"].(map[string]any); ok {
		if name := stringField(author, "name"); name != "" {
			fmt.Printf("  Author:      %s\n", name)
		}
	}
	if license := stringField(plugin, "license"); license != "" {
		fmt.Printf("  License:     %s\n", license)
	}

	// Show capabilities
	if caps, ok := plugin["capabilities"].(map[string]any); ok {
		fmt.Println("\nCapabilities:")
		if tools, ok := caps["tools"].([]any); ok && len(tools) > 0 {
			fmt.Printf("  Tools:           %d\n", len(tools))
		}
		if channels, ok := caps["channels"].([]any); ok && len(channels) > 0 {
			fmt.Printf("  Channels:        %d\n", len(channels))
		}
		if mcp, ok := caps["mcp_servers"].([]any); ok && len(mcp) > 0 {
			fmt.Printf("  MCP Servers:     %d\n", len(mcp))
		}
		if skills, ok := caps["skills"].([]any); ok && len(skills) > 0 {
			fmt.Printf("  Skills:          %d\n", len(skills))
		}
		if hooks, ok := caps["hooks"].([]any); ok && len(hooks) > 0 {
			fmt.Printf("  Hooks:           %d\n", len(hooks))
		}
		if methods, ok := caps["gateway_methods"].([]any); ok && len(methods) > 0 {
			fmt.Printf("  Gateway Methods: %d\n", len(methods))
		}
	}

	// Show permissions
	if perms, ok := plugin["permissions"].(map[string]any); ok && len(perms) > 0 {
		fmt.Println("\nPermissions:")
		if net, ok := perms["network"].(map[string]any); ok {
			if hosts, ok := net["hosts"].([]any); ok && len(hosts) > 0 {
				fmt.Printf("  Network:     %d hosts\n", len(hosts))
			} else if allowAll, _ := net["allow_all"].(bool); allowAll {
				fmt.Printf("  Network:     all hosts\n")
			}
		}
		if fs, ok := perms["filesystem"].(map[string]any); ok {
			if read, ok := fs["read"].([]any); ok && len(read) > 0 {
				fmt.Printf("  Filesystem:  read %d paths\n", len(read))
			}
			if write, ok := fs["write"].([]any); ok && len(write) > 0 {
				fmt.Printf("  Filesystem:  write %d paths\n", len(write))
			}
		}
		if storage, _ := perms["storage"].(bool); storage {
			fmt.Printf("  Storage:     yes\n")
		}
		if agent, _ := perms["agent"].(bool); agent {
			fmt.Printf("  Agent:       yes\n")
		}
	}

	return nil
}

func runPluginsCapabilities(args []string) error {
	fs := flag.NewFlagSet("plugins capabilities", flag.ContinueOnError)
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

	result, err := cl.call("plugins.capabilities", map[string]any{})
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(result)
	}

	fmt.Println("Plugin Capabilities Summary")
	fmt.Println(strings.Repeat("─", 40))

	if count, ok := result["plugin_count"].(float64); ok {
		fmt.Printf("Plugins:         %d\n", int(count))
	}
	if count, ok := result["tool_count"].(float64); ok {
		fmt.Printf("Tools:           %d\n", int(count))
	}
	if count, ok := result["channel_count"].(float64); ok {
		fmt.Printf("Channels:        %d\n", int(count))
	}
	if count, ok := result["mcp_count"].(float64); ok {
		fmt.Printf("MCP Servers:     %d\n", int(count))
	}
	if count, ok := result["skill_count"].(float64); ok {
		fmt.Printf("Skills:          %d\n", int(count))
	}
	if count, ok := result["method_count"].(float64); ok {
		fmt.Printf("Gateway Methods: %d\n", int(count))
	}
	if count, ok := result["hook_count"].(float64); ok {
		fmt.Printf("Hooks:           %d\n", int(count))
	}

	if tools, ok := result["tools"].([]any); ok && len(tools) > 0 {
		fmt.Println("\nTools:")
		for _, t := range tools {
			if ts, ok := t.(string); ok {
				fmt.Printf("  %s\n", ts)
			}
		}
	}

	if channels, ok := result["channels"].([]any); ok && len(channels) > 0 {
		fmt.Println("\nChannels:")
		for _, c := range channels {
			if cs, ok := c.(string); ok {
				fmt.Printf("  %s\n", cs)
			}
		}
	}

	if mcp, ok := result["mcp_servers"].([]any); ok && len(mcp) > 0 {
		fmt.Println("\nMCP Servers:")
		for _, m := range mcp {
			if ms, ok := m.(string); ok {
				fmt.Printf("  %s\n", ms)
			}
		}
	}

	if skills, ok := result["skills"].([]any); ok && len(skills) > 0 {
		fmt.Println("\nSkills:")
		for _, s := range skills {
			if ss, ok := s.(string); ok {
				fmt.Printf("  %s\n", ss)
			}
		}
	}

	return nil
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
