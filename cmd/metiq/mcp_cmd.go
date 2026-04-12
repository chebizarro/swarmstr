package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"text/tabwriter"
)

// ─── mcp auth ────────────────────────────────────────────────────────────────

func runMCP(args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "mcp subcommands: list, get, put, add, remove, test, reconnect, auth\n")
		return fmt.Errorf("missing subcommand")
	}
	switch args[0] {
	case "list", "ls":
		return runMCPList(args[1:])
	case "get", "show":
		return runMCPGet(args[1:])
	case "put", "set":
		return runMCPPut(args[1:])
	case "add":
		return runMCPPut(args[1:])
	case "remove", "rm", "delete":
		return runMCPRemove(args[1:])
	case "test":
		return runMCPTest(args[1:])
	case "reconnect":
		return runMCPReconnect(args[1:])
	case "auth":
		return runMCPAuth(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "mcp subcommands: list, get, put, add, remove, test, reconnect, auth\n")
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func normalizeLeadingPositionalArg(args []string) []string {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return args
	}
	reordered := append([]string{}, args[1:]...)
	reordered = append(reordered, args[0])
	return reordered
}

func runMCPList(args []string) error {
	fs := flag.NewFlagSet("mcp list", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: metiq mcp list")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("mcp.list", map[string]any{})
	if err != nil {
		return err
	}
	if jsonOut {
		if err := printJSON(result); err != nil {
			return err
		}
		return nil
	}
	servers := mcpServers(result)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATE\tENABLED\tTRANSPORT\tTOOLS\tAUTH\tTARGET")
	for _, server := range servers {
		fmt.Fprintf(w, "%s\t%s\t%t\t%s\t%d\t%s\t%s\n",
			stringFieldAny(server, "name"),
			stringFieldAny(server, "state"),
			boolFieldAny(server, "enabled"),
			stringFieldAny(server, "transport"),
			intFieldAny(server, "tool_count"),
			mcpAuthStatus(server),
			mcpTarget(server),
		)
	}
	_ = w.Flush()
	if suppressed := len(anySlice(result["suppressed"])); suppressed > 0 {
		fmt.Printf("suppressed: %d\n", suppressed)
	}
	return nil
}

func runMCPGet(args []string) error {
	args = normalizeLeadingPositionalArg(args)
	fs := flag.NewFlagSet("mcp get", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: metiq mcp get <server>")
	}
	serverName := strings.TrimSpace(fs.Arg(0))
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("mcp.get", map[string]any{"server": serverName})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	server := mcpServer(result)
	if len(server) == 0 {
		return fmt.Errorf("server payload missing")
	}
	printMCPServer(server)
	return nil
}

func runMCPPut(args []string) error {
	serverName, config, jsonOut, cl, err := prepareMCPMutationCommand("mcp put", args)
	if err != nil {
		return err
	}
	result, err := cl.call("mcp.put", map[string]any{"server": serverName, "config": config})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	server := mcpServer(result)
	fmt.Printf("updated mcp server %s state=%s transport=%s\n", serverName, stringFieldAny(server, "state"), stringFieldAny(server, "transport"))
	return nil
}

func runMCPRemove(args []string) error {
	args = normalizeLeadingPositionalArg(args)
	fs := flag.NewFlagSet("mcp remove", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: metiq mcp remove <server>")
	}
	serverName := strings.TrimSpace(fs.Arg(0))
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("mcp.remove", map[string]any{"server": serverName})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	fmt.Printf("removed mcp server %s\n", serverName)
	return nil
}

func runMCPTest(args []string) error {
	serverName, config, jsonOut, cl, timeoutMS, err := prepareMCPProbeCommand("mcp test", args)
	if err != nil {
		return err
	}
	params := map[string]any{"server": serverName}
	if len(config) > 0 {
		params["config"] = config
	}
	if timeoutMS > 0 {
		params["timeout_ms"] = timeoutMS
	}
	result, err := cl.call("mcp.test", params)
	if err != nil {
		return err
	}
	if jsonOut {
		if err := printJSON(result); err != nil {
			return err
		}
		if !boolFieldAny(result, "ok") {
			return fmt.Errorf("%s", stringFieldAny(result, "error"))
		}
		return nil
	}
	server := mcpServer(result)
	if boolFieldAny(result, "ok") {
		fmt.Printf("mcp test passed for %s state=%s transport=%s\n", serverName, stringFieldAny(server, "state"), stringFieldAny(server, "transport"))
		return nil
	}
	fmt.Printf("mcp test failed for %s state=%s error=%s\n", serverName, stringFieldAny(server, "state"), stringFieldAny(result, "error"))
	return fmt.Errorf("%s", stringFieldAny(result, "error"))
}

func runMCPReconnect(args []string) error {
	args = normalizeLeadingPositionalArg(args)
	fs := flag.NewFlagSet("mcp reconnect", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: metiq mcp reconnect <server>")
	}
	serverName := strings.TrimSpace(fs.Arg(0))
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("mcp.reconnect", map[string]any{"server": serverName})
	if err != nil {
		return err
	}
	if jsonOut {
		if err := printJSON(result); err != nil {
			return err
		}
		if !boolFieldAny(result, "ok") {
			return fmt.Errorf("%s", stringFieldAny(result, "error"))
		}
		return nil
	}
	server := mcpServer(result)
	if boolFieldAny(result, "ok") {
		fmt.Printf("reconnected mcp server %s state=%s\n", serverName, stringFieldAny(server, "state"))
		return nil
	}
	fmt.Printf("mcp reconnect failed for %s state=%s error=%s\n", serverName, stringFieldAny(server, "state"), stringFieldAny(result, "error"))
	return fmt.Errorf("%s", stringFieldAny(result, "error"))
}

func runMCPAuth(args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "mcp auth subcommands: start, refresh, clear\n")
		return fmt.Errorf("missing subcommand")
	}
	switch args[0] {
	case "start":
		return runMCPAuthStart(args[1:])
	case "refresh":
		return runMCPAuthRefresh(args[1:])
	case "clear":
		return runMCPAuthClear(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "mcp auth subcommands: start, refresh, clear\n")
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func runMCPAuthStart(args []string) error {
	args = normalizeLeadingPositionalArg(args)
	fs := flag.NewFlagSet("mcp auth start", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, clientSecret string
	var jsonOut, openBrowser bool
	var timeoutMS int
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&clientSecret, "client-secret", "", "optional oauth client secret to persist outside config")
	fs.IntVar(&timeoutMS, "timeout-ms", 0, "optional auth flow timeout in milliseconds")
	fs.BoolVar(&openBrowser, "open", true, "open the returned authorize URL in the default browser")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: metiq mcp auth start <server>")
	}
	server := strings.TrimSpace(fs.Arg(0))
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	params := map[string]any{"server": server}
	if strings.TrimSpace(clientSecret) != "" {
		params["client_secret"] = clientSecret
	}
	if timeoutMS > 0 {
		params["timeout_ms"] = timeoutMS
	}
	result, err := cl.call("mcp.auth.start", params)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	authorizeURL := stringField(result, "authorize_url")
	callbackURL := stringField(result, "callback_url")
	fmt.Printf("server:        %s\n", server)
	fmt.Printf("authorize_url: %s\n", authorizeURL)
	fmt.Printf("callback_url:  %s\n", callbackURL)
	if openBrowser && authorizeURL != "" {
		if err := openBrowserURL(authorizeURL); err != nil {
			return fmt.Errorf("open browser: %w", err)
		}
		fmt.Println("browser:       opened")
	}
	return nil
}

func runMCPAuthRefresh(args []string) error {
	args = normalizeLeadingPositionalArg(args)
	fs := flag.NewFlagSet("mcp auth refresh", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: metiq mcp auth refresh <server>")
	}
	server := strings.TrimSpace(fs.Arg(0))
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("mcp.auth.refresh", map[string]any{"server": server})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	fmt.Printf("refreshed auth for %s\n", server)
	return nil
}

func runMCPAuthClear(args []string) error {
	args = normalizeLeadingPositionalArg(args)
	fs := flag.NewFlagSet("mcp auth clear", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: metiq mcp auth clear <server>")
	}
	server := strings.TrimSpace(fs.Arg(0))
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("mcp.auth.clear", map[string]any{"server": server})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	fmt.Printf("cleared auth for %s\n", server)
	return nil
}

func prepareMCPMutationCommand(name string, args []string) (string, map[string]any, bool, gatewayCaller, error) {
	args = normalizeLeadingPositionalArg(args)
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var transport, command, url string
	var disabled, jsonOut bool
	var argList stringListFlag
	var env keyValueFlag
	var headers keyValueFlag
	var oauthScopes stringListFlag
	var oauthClientID, oauthAuthorizeURL, oauthTokenURL, oauthClientSecretRef string
	var oauthUsePKCE bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&transport, "transport", "", "transport: stdio, sse, or http")
	fs.StringVar(&command, "command", "", "stdio command")
	fs.StringVar(&url, "url", "", "remote MCP URL")
	fs.BoolVar(&disabled, "disabled", false, "store the server disabled in config")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	fs.Var(&argList, "arg", "repeatable stdio arg")
	fs.Var(&env, "env", "repeatable KEY=VALUE environment override")
	fs.Var(&headers, "header", "repeatable KEY=VALUE request header")
	fs.StringVar(&oauthClientID, "oauth-client-id", "", "oauth client id")
	fs.StringVar(&oauthAuthorizeURL, "oauth-authorize-url", "", "oauth authorize URL")
	fs.StringVar(&oauthTokenURL, "oauth-token-url", "", "oauth token URL")
	fs.StringVar(&oauthClientSecretRef, "oauth-client-secret-ref", "", "oauth client secret ref (env:NAME, $NAME, etc)")
	fs.BoolVar(&oauthUsePKCE, "oauth-use-pkce", false, "enable PKCE for OAuth")
	fs.Var(&oauthScopes, "oauth-scope", "repeatable oauth scope")
	if err := fs.Parse(args); err != nil {
		return "", nil, false, nil, err
	}
	if fs.NArg() != 1 {
		return "", nil, false, nil, fmt.Errorf("usage: metiq %s <server> [flags]", name)
	}
	serverName := strings.TrimSpace(fs.Arg(0))
	config, err := buildMCPServerConfig(transport, command, url, []string(argList), map[string]string(env), map[string]string(headers), disabled, oauthClientID, oauthAuthorizeURL, oauthTokenURL, oauthClientSecretRef, []string(oauthScopes), oauthUsePKCE)
	if err != nil {
		return "", nil, false, nil, err
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return "", nil, false, nil, err
	}
	return serverName, config, jsonOut, cl, nil
}

func prepareMCPProbeCommand(name string, args []string) (string, map[string]any, bool, gatewayCaller, int, error) {
	args = normalizeLeadingPositionalArg(args)
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var transport, command, url string
	var jsonOut bool
	var timeoutMS int
	var argList stringListFlag
	var env keyValueFlag
	var headers keyValueFlag
	var oauthScopes stringListFlag
	var oauthClientID, oauthAuthorizeURL, oauthTokenURL, oauthClientSecretRef string
	var oauthUsePKCE bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&transport, "transport", "", "transport: stdio, sse, or http")
	fs.StringVar(&command, "command", "", "stdio command")
	fs.StringVar(&url, "url", "", "remote MCP URL")
	fs.IntVar(&timeoutMS, "timeout-ms", 0, "optional test timeout in milliseconds")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	fs.Var(&argList, "arg", "repeatable stdio arg")
	fs.Var(&env, "env", "repeatable KEY=VALUE environment override")
	fs.Var(&headers, "header", "repeatable KEY=VALUE request header")
	fs.StringVar(&oauthClientID, "oauth-client-id", "", "oauth client id")
	fs.StringVar(&oauthAuthorizeURL, "oauth-authorize-url", "", "oauth authorize URL")
	fs.StringVar(&oauthTokenURL, "oauth-token-url", "", "oauth token URL")
	fs.StringVar(&oauthClientSecretRef, "oauth-client-secret-ref", "", "oauth client secret ref (env:NAME, $NAME, etc)")
	fs.BoolVar(&oauthUsePKCE, "oauth-use-pkce", false, "enable PKCE for OAuth")
	fs.Var(&oauthScopes, "oauth-scope", "repeatable oauth scope")
	if err := fs.Parse(args); err != nil {
		return "", nil, false, nil, 0, err
	}
	if fs.NArg() != 1 {
		return "", nil, false, nil, 0, fmt.Errorf("usage: metiq %s <server> [flags]", name)
	}
	serverName := strings.TrimSpace(fs.Arg(0))
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return "", nil, false, nil, 0, err
	}
	hasInlineConfig := strings.TrimSpace(transport) != "" || strings.TrimSpace(command) != "" || strings.TrimSpace(url) != "" || len(argList) > 0 || len(env) > 0 || len(headers) > 0 || strings.TrimSpace(oauthClientID) != "" || strings.TrimSpace(oauthAuthorizeURL) != "" || strings.TrimSpace(oauthTokenURL) != "" || strings.TrimSpace(oauthClientSecretRef) != "" || len(oauthScopes) > 0 || oauthUsePKCE
	if !hasInlineConfig {
		return serverName, nil, jsonOut, cl, timeoutMS, nil
	}
	config, err := buildMCPServerConfig(transport, command, url, []string(argList), map[string]string(env), map[string]string(headers), false, oauthClientID, oauthAuthorizeURL, oauthTokenURL, oauthClientSecretRef, []string(oauthScopes), oauthUsePKCE)
	if err != nil {
		return "", nil, false, nil, 0, err
	}
	return serverName, config, jsonOut, cl, timeoutMS, nil
}

func buildMCPServerConfig(transport, command, url string, args []string, env, headers map[string]string, disabled bool, oauthClientID, oauthAuthorizeURL, oauthTokenURL, oauthClientSecretRef string, oauthScopes []string, oauthUsePKCE bool) (map[string]any, error) {
	transport = strings.ToLower(strings.TrimSpace(transport))
	command = strings.TrimSpace(command)
	url = strings.TrimSpace(url)
	if transport != "" && transport != "stdio" && transport != "sse" && transport != "http" {
		return nil, fmt.Errorf("transport must be one of stdio, sse, or http")
	}
	if command != "" && url != "" {
		return nil, fmt.Errorf("command and url are mutually exclusive")
	}
	if transport == "" {
		switch {
		case command != "":
			transport = "stdio"
		case url != "":
			transport = "sse"
		default:
			return nil, fmt.Errorf("one of --command or --url is required")
		}
	}
	if transport == "stdio" && command == "" {
		return nil, fmt.Errorf("--command is required for stdio transport")
	}
	if (transport == "sse" || transport == "http") && url == "" {
		return nil, fmt.Errorf("--url is required for remote MCP transport")
	}
	config := map[string]any{"enabled": !disabled, "type": transport}
	if command != "" {
		config["command"] = command
	}
	if len(args) > 0 {
		config["args"] = args
	}
	if len(env) > 0 {
		config["env"] = env
	}
	if url != "" {
		config["url"] = url
	}
	if len(headers) > 0 {
		config["headers"] = headers
	}
	oauthEnabled := strings.TrimSpace(oauthClientID) != "" || strings.TrimSpace(oauthAuthorizeURL) != "" || strings.TrimSpace(oauthTokenURL) != "" || strings.TrimSpace(oauthClientSecretRef) != "" || len(oauthScopes) > 0 || oauthUsePKCE
	if oauthEnabled {
		if transport != "sse" && transport != "http" {
			return nil, fmt.Errorf("oauth flags require remote MCP transport")
		}
		if strings.TrimSpace(oauthClientID) == "" || strings.TrimSpace(oauthAuthorizeURL) == "" || strings.TrimSpace(oauthTokenURL) == "" {
			return nil, fmt.Errorf("oauth requires client id, authorize url, and token url")
		}
		config["oauth"] = map[string]any{
			"enabled":           true,
			"client_id":         strings.TrimSpace(oauthClientID),
			"authorize_url":     strings.TrimSpace(oauthAuthorizeURL),
			"token_url":         strings.TrimSpace(oauthTokenURL),
			"client_secret_ref": strings.TrimSpace(oauthClientSecretRef),
			"scopes":            oauthScopes,
			"use_pkce":          oauthUsePKCE,
		}
	}
	return config, nil
}

func mcpServers(result map[string]any) []map[string]any {
	items := anySlice(result["servers"])
	servers := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if server, ok := item.(map[string]any); ok {
			servers = append(servers, server)
		}
	}
	return servers
}

func mcpServer(result map[string]any) map[string]any {
	server, _ := result["server"].(map[string]any)
	return server
}

func mcpTarget(server map[string]any) string {
	if url := stringFieldAny(server, "url"); url != "" {
		return url
	}
	return stringFieldAny(server, "command")
}

func mcpAuthStatus(server map[string]any) string {
	if !boolFieldAny(server, "oauth_configured") {
		return "-"
	}
	if boolFieldAny(server, "has_credentials") {
		return "stored"
	}
	if stringFieldAny(server, "state") == "needs-auth" {
		return "needed"
	}
	return "configured"
}

func printMCPServer(server map[string]any) {
	fmt.Printf("name: %s\n", stringFieldAny(server, "name"))
	fmt.Printf("state: %s\n", stringFieldAny(server, "state"))
	fmt.Printf("enabled: %t\n", boolFieldAny(server, "enabled"))
	if transport := stringFieldAny(server, "transport"); transport != "" {
		fmt.Printf("transport: %s\n", transport)
	}
	if command := stringFieldAny(server, "command"); command != "" {
		fmt.Printf("command: %s\n", command)
	}
	if url := stringFieldAny(server, "url"); url != "" {
		fmt.Printf("url: %s\n", url)
	}
	fmt.Printf("tool_count: %d\n", intFieldAny(server, "tool_count"))
	if source := stringFieldAny(server, "source"); source != "" {
		fmt.Printf("source: %s\n", source)
	}
	if keys := stringSliceAny(server["env_keys"]); len(keys) > 0 {
		fmt.Printf("env_keys: %s\n", strings.Join(keys, ", "))
	}
	if keys := stringSliceAny(server["header_keys"]); len(keys) > 0 {
		fmt.Printf("header_keys: %s\n", strings.Join(keys, ", "))
	}
	fmt.Printf("oauth: %s\n", mcpAuthStatus(server))
	if lastErr := stringFieldAny(server, "last_error"); lastErr != "" {
		fmt.Printf("last_error: %s\n", lastErr)
	}
}

func openBrowserURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return fmt.Errorf("url is required")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Start()
}
