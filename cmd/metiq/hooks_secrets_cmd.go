package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

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
