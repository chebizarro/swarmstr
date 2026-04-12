package main

import (
	"flag"
	"fmt"
	"os"
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
