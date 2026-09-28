package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"metiq/internal/secrets"
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
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
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
		fmt.Fprintf(os.Stderr, "secrets subcommands: list, get, set, migrate\n")
		return fmt.Errorf("missing subcommand")
	}
	switch args[0] {
	case "list", "ls":
		return runSecretsList(args[1:])
	case "get":
		return runSecretsGet(args[1:])
	case "set":
		return runSecretsSet(args[1:])
	case "migrate":
		return runSecretsMigrate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "secrets subcommands: list, get, set, migrate\n")
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
	return secretsSet(args, os.Stdin, os.Stdout)
}

// secretsSet stores a value in the daemon's protected gateway-store via
// secrets.store.set. The value is read from stdin so it never appears in argv
// or shell history.
func secretsSet(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("secrets set", flag.ContinueOnError)
	var wsURL, wsToken, bootstrapPath, kind string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path (supplies gateway_ws_listen_addr/gateway_ws_token)")
	fs.StringVar(&wsURL, "ws-url", "", "gateway websocket URL, e.g. ws://127.0.0.1:8788/ws")
	fs.StringVar(&wsToken, "ws-token", "", "gateway websocket token")
	fs.StringVar(&kind, "kind", "secret", "entry kind: secret (resolved only through secret refs) or env")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: metiq secrets set [--kind secret|env] <NAME> < value-file  (the value is read from stdin)")
	}
	name := fs.Arg(0)
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("read secret value from stdin: %w", err)
	}
	value := strings.TrimRight(string(raw), "\r\n")
	if value == "" {
		return fmt.Errorf("secret value is empty; pipe it on stdin")
	}
	url, token, err := resolveGatewayWSURL(wsURL, wsToken, bootstrapPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	params := map[string]any{"name": name, "value": value, "kind": kind}
	if _, err := gatewayWSCall(ctx, url, token, "secrets.store.set", params); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "stored %s; reference it in config as {\"source\":\"store\",\"provider\":\"gateway-store\",\"id\":%q}\n", name, name)
	return nil
}

func runSecretsMigrate(args []string) error {
	fs := flag.NewFlagSet("secrets migrate", flag.ContinueOnError)
	var configPath string
	var jsonOut bool
	fs.StringVar(&configPath, "config", "", "config JSON file to scan")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "print migration plan as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if configPath == "" && fs.NArg() > 0 {
		configPath = fs.Arg(0)
	}
	if configPath == "" {
		return fmt.Errorf("usage: metiq secrets migrate --config <config.json> [--json]")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}
	plan := secrets.PlanMigration(root)
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(plan)
	}
	if len(plan.Changes) == 0 {
		fmt.Println("no inline secrets detected")
		return nil
	}
	for _, change := range plan.Changes {
		fmt.Printf("%s -> %s (store as %s)\n", change.Path, change.Replacement, change.SecretName)
	}
	return nil
}
