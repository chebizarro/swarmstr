package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"metiq/internal/logging"
	pluginmanifest "metiq/internal/plugins/manifest"
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
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
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
		printMuted("No agents configured")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, logging.Theme.Heading("ID\tMODEL\tSTATUS"))
	for _, a := range agents {
		ag, ok := a.(map[string]any)
		if !ok {
			continue
		}
		id := stringField(ag, "id")
		model := stringField(ag, "model")
		status := stringField(ag, "status")

		statusColor := logging.Theme.Info
		if status == "active" || status == "running" {
			statusColor = logging.Theme.Success
		} else if status == "error" || status == "failed" {
			statusColor = logging.Theme.Error
		}

		fmt.Fprintf(w, "%s\t%s\t%s\n",
			logging.Theme.AccentBright(id),
			logging.Theme.Muted(model),
			statusColor(status))
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
		return runPluginsInstall(args[1:])
	case "build", "bundle", "package":
		return runPluginsBuild(args[1:])
	case "enable":
		return runPluginsSetEnabled(args[1:], true)
	case "disable":
		return runPluginsSetEnabled(args[1:], false)
	case "update":
		return runPluginsUpdate(args[1:])
	case "uninstall", "remove", "rm":
		return runPluginsUninstall(args[1:])
	case "doctor":
		return runPluginsDoctor(args[1:])
	case "validate":
		return runPluginsValidate(args[1:])
	case "registry", "marketplace":
		return runPluginsRegistry(args[1:])
	case "search":
		return runPluginSearch("", args[1:])
	case "publish":
		return runPluginPublish("", args[1:])
	default:
		fmt.Fprintf(os.Stderr, "plugins subcommands: list, info, capabilities, install, build, enable, disable, update, uninstall, doctor, validate, registry, search, publish\n")
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func runPluginsBuild(args []string) error {
	fs := flag.NewFlagSet("plugins build", flag.ContinueOnError)
	var sourceDir, manifestPath, outPath string
	var skipNPM, jsonOut bool
	fs.StringVar(&sourceDir, "dir", "", "plugin source directory (default: positional arg or .)")
	fs.StringVar(&manifestPath, "manifest", "", "manifest path (default: <dir>/manifest.json)")
	fs.StringVar(&outPath, "out", "", "output package path (default: <dir>/dist/<id>-<version>.zip)")
	fs.BoolVar(&skipNPM, "skip-npm", false, "skip npm build even when package.json is present")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if sourceDir == "" {
		if fs.NArg() > 0 {
			sourceDir = fs.Arg(0)
		} else {
			sourceDir = "."
		}
	}
	absDir, err := filepath.Abs(sourceDir)
	if err != nil {
		return fmt.Errorf("resolve source dir: %w", err)
	}
	st, err := os.Stat(absDir)
	if err != nil {
		return fmt.Errorf("stat source dir: %w", err)
	}
	if !st.IsDir() {
		return fmt.Errorf("plugin source must be a directory: %s", absDir)
	}
	if manifestPath == "" {
		manifestPath = filepath.Join(absDir, "manifest.json")
	} else if !filepath.IsAbs(manifestPath) {
		manifestPath = filepath.Join(absDir, manifestPath)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var mf pluginmanifest.Manifest
	if err := json.Unmarshal(data, &mf); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	if err := pluginmanifest.Validate(&mf); err != nil {
		return fmt.Errorf("validate manifest: %w", err)
	}
	pkgJSON := filepath.Join(absDir, "package.json")
	if !skipNPM && mf.Runtime == pluginmanifest.RuntimeNode && fileExists(pkgJSON) {
		cmd := exec.Command("npm", "run", "build")
		cmd.Dir = absDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("npm run build failed: %w", err)
		}
	}
	if outPath == "" {
		outPath = filepath.Join(absDir, "dist", fmt.Sprintf("%s-%s.zip", mf.ID, mf.Version))
	} else if !filepath.IsAbs(outPath) {
		outPath = filepath.Join(absDir, outPath)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	files, bytesWritten, err := zipPluginDirectory(absDir, outPath)
	if err != nil {
		return err
	}
	checksum, err := sha256File(outPath)
	if err != nil {
		return err
	}
	result := map[string]any{"ok": true, "plugin_id": mf.ID, "version": mf.Version, "runtime": mf.Runtime, "package": outPath, "files": files, "bytes": bytesWritten, "sha256": checksum}
	if jsonOut {
		return printJSON(result)
	}
	fmt.Printf("plugin built %s v%s → %s\n", mf.ID, mf.Version, outPath)
	fmt.Printf("  files: %d bytes: %d sha256:%s\n", files, bytesWritten, checksum)
	return nil
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func zipPluginDirectory(root, outPath string) (int, int64, error) {
	out, err := os.Create(outPath)
	if err != nil {
		return 0, 0, fmt.Errorf("create package: %w", err)
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	defer zw.Close()
	var files int
	var total int64
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if shouldSkipPluginBuildPath(rel, d.IsDir(), outPath, path) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		header.Method = zip.Deflate
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(writer, in)
		closeErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		files++
		total += n
		return nil
	})
	if err != nil {
		return 0, 0, fmt.Errorf("package plugin: %w", err)
	}
	return files, total, nil
}

func shouldSkipPluginBuildPath(rel string, isDir bool, outPath, path string) bool {
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	if absOut, err := filepath.Abs(outPath); err == nil {
		if absPath, err := filepath.Abs(path); err == nil && absOut == absPath {
			return true
		}
	}
	if isDir {
		switch base {
		case ".git", "node_modules", ".metiq", "tmp", "temp":
			return true
		}
		if rel == "dist" || strings.HasPrefix(rel, "dist/") {
			return true
		}
	}
	return strings.HasSuffix(rel, ".zip") || strings.HasSuffix(rel, ".tgz") || strings.HasSuffix(rel, ".tar.gz")
}

func sha256File(path string) (string, error) {
	in, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open package for checksum: %w", err)
	}
	defer in.Close()
	h := sha256.New()
	if _, err := io.Copy(h, in); err != nil {
		return "", fmt.Errorf("hash package: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hasFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}

func addPluginAdminFlags(fs *flag.FlagSet, bootstrapPath, adminAddr, adminToken *string, jsonOut *bool) {
	fs.StringVar(bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(adminToken, "admin-token", "", "admin API bearer token")
	if jsonOut != nil {
		fs.BoolVar(jsonOut, "json", jsonFlagDefault(), "output raw JSON")
	}
}

func runPluginsInstall(args []string) error {
	// Preserve the legacy Nostr registry flow when users pass --pubkey.
	if hasFlag(args, "--pubkey") {
		return runPluginInstall("", args)
	}
	fs := flag.NewFlagSet("plugins install", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, pluginID, source, spec, sourcePath, installPath, url, ref string
	var enableEntry, includeLoadPath, jsonOut bool
	addPluginAdminFlags(fs, &bootstrapPath, &adminAddr, &adminToken, &jsonOut)
	fs.StringVar(&pluginID, "id", "", "plugin ID")
	fs.StringVar(&source, "source", "", "install source: npm, git, url, archive, path")
	fs.StringVar(&spec, "spec", "", "source spec (npm package or git repo)")
	fs.StringVar(&url, "url", "", "URL for source=url or source=git")
	fs.StringVar(&ref, "ref", "", "git ref/tag/branch")
	fs.StringVar(&sourcePath, "source-path", "", "local source path/archive path")
	fs.StringVar(&sourcePath, "sourcePath", "", "local source path/archive path")
	fs.StringVar(&installPath, "install-path", "", "managed install path (default ./extensions/<id>)")
	fs.StringVar(&installPath, "installPath", "", "managed install path (default ./extensions/<id>)")
	fs.BoolVar(&enableEntry, "enable", true, "enable plugin entry after install")
	fs.BoolVar(&includeLoadPath, "include-load-path", true, "add path plugins to load_paths")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if pluginID == "" && fs.NArg() > 0 {
		pluginID = fs.Arg(0)
	}
	source = strings.ToLower(strings.TrimSpace(source))
	if pluginID == "" || source == "" {
		return fmt.Errorf("usage: metiq plugins install --id <plugin-id> --source npm|git|url|archive|path [--spec <spec>|--url <url>|--source-path <path>]")
	}
	install := map[string]any{"source": source}
	if spec != "" {
		install["spec"] = spec
	}
	if url != "" {
		install["url"] = url
	}
	if ref != "" {
		install["ref"] = ref
	}
	if sourcePath != "" {
		install["sourcePath"] = sourcePath
	}
	if installPath != "" {
		install["installPath"] = installPath
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("plugins.install", map[string]any{
		"plugin_id":         pluginID,
		"install":           install,
		"enable_entry":      enableEntry,
		"include_load_path": includeLoadPath,
	})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	fmt.Printf("plugin installed %s from %s\n", pluginID, source)
	return nil
}

func runPluginsSetEnabled(args []string, enabled bool) error {
	name := "enable"
	if !enabled {
		name = "disable"
	}
	fs := flag.NewFlagSet("plugins "+name, flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	addPluginAdminFlags(fs, &bootstrapPath, &adminAddr, &adminToken, &jsonOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq plugins %s <plugin-id>", name)
	}
	pluginID := strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("config.set", map[string]any{"key": "plugins.entries." + pluginID + ".enabled", "value": enabled})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	if enabled {
		fmt.Printf("plugin enabled %s\n", pluginID)
	} else {
		fmt.Printf("plugin disabled %s\n", pluginID)
	}
	return nil
}

func runPluginsUpdate(args []string) error {
	fs := flag.NewFlagSet("plugins update", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut, dryRun bool
	addPluginAdminFlags(fs, &bootstrapPath, &adminAddr, &adminToken, &jsonOut)
	fs.BoolVar(&dryRun, "dry-run", false, "preview updates without changing config")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ids := []string{}
	for _, arg := range fs.Args() {
		if trimmed := strings.TrimSpace(arg); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("plugins.update", map[string]any{"plugin_ids": ids, "dry_run": dryRun})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	if outcomes, ok := result["outcomes"].([]any); ok {
		for _, item := range outcomes {
			if m, ok := item.(map[string]any); ok {
				fmt.Printf("%s\t%s\t%s\n", stringField(m, "pluginId"), stringField(m, "status"), stringField(m, "message"))
			}
		}
	}
	return nil
}

func runPluginsUninstall(args []string) error {
	fs := flag.NewFlagSet("plugins uninstall", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	addPluginAdminFlags(fs, &bootstrapPath, &adminAddr, &adminToken, &jsonOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq plugins uninstall <plugin-id>")
	}
	pluginID := strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("plugins.uninstall", map[string]any{"plugin_id": pluginID})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	fmt.Printf("plugin uninstalled %s\n", pluginID)
	return nil
}

func runPluginsDoctor(args []string) error {
	fs := flag.NewFlagSet("plugins doctor", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	addPluginAdminFlags(fs, &bootstrapPath, &adminAddr, &adminToken, &jsonOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	list, listErr := cl.call("plugins.list", map[string]any{})
	caps, capsErr := cl.call("plugins.capabilities", map[string]any{})
	result := map[string]any{"ok": listErr == nil && capsErr == nil, "list_error": "", "capabilities_error": "", "plugins": list, "capabilities": caps}
	if listErr != nil {
		result["list_error"] = listErr.Error()
	}
	if capsErr != nil {
		result["capabilities_error"] = capsErr.Error()
	}
	if jsonOut {
		return printJSON(result)
	}
	if result["ok"] == true {
		fmt.Println("plugin doctor: ok")
		return nil
	}
	return fmt.Errorf("plugin doctor failed: list=%v capabilities=%v", result["list_error"], result["capabilities_error"])
}

func runPluginsValidate(args []string) error {
	fs := flag.NewFlagSet("plugins validate", flag.ContinueOnError)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq plugins validate <manifest.json>")
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var mf pluginmanifest.Manifest
	err = json.Unmarshal(data, &mf)
	if err == nil {
		err = pluginmanifest.Validate(&mf)
	}
	result := map[string]any{"ok": err == nil, "path": fs.Arg(0)}
	if err != nil {
		result["error"] = err.Error()
	}
	if jsonOut {
		return printJSON(result)
	}
	if err != nil {
		return err
	}
	fmt.Printf("manifest valid %s\n", fs.Arg(0))
	return nil
}

func runPluginsRegistry(args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list", "ls":
		return runPluginsRegistryCall("plugins.registry.list", args[1:])
	case "search":
		return runPluginsRegistryCall("plugins.registry.search", args[1:])
	case "get", "info", "show":
		return runPluginsRegistryCall("plugins.registry.get", args[1:])
	default:
		return fmt.Errorf("plugins registry subcommands: list, search, get")
	}
}

func runPluginsRegistryCall(method string, args []string) error {
	fs := flag.NewFlagSet("plugins registry", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, registryURL, query, tag string
	var jsonOut bool
	addPluginAdminFlags(fs, &bootstrapPath, &adminAddr, &adminToken, &jsonOut)
	fs.StringVar(&registryURL, "registry", "", "registry index URL")
	fs.StringVar(&registryURL, "registry-url", "", "registry index URL")
	fs.StringVar(&query, "q", "", "search query")
	fs.StringVar(&tag, "tag", "", "tag filter")
	if err := fs.Parse(args); err != nil {
		return err
	}
	params := map[string]any{}
	if registryURL != "" {
		params["registry_url"] = registryURL
	}
	if method == "plugins.registry.get" {
		if fs.NArg() == 0 {
			return fmt.Errorf("usage: metiq plugins registry get <plugin-id>")
		}
		params["plugin_id"] = fs.Arg(0)
	}
	if method == "plugins.registry.search" {
		if query == "" && fs.NArg() > 0 {
			query = strings.Join(fs.Args(), " ")
		}
		params["query"] = query
		params["tag"] = tag
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call(method, params)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	plugins, _ := result["plugins"].([]any)
	if method == "plugins.registry.get" {
		if p, ok := result["plugin"].(map[string]any); ok {
			plugins = []any{p}
		}
	}
	for _, item := range plugins {
		if p, ok := item.(map[string]any); ok {
			fmt.Printf("%s\t%s\t%s\n", stringField(p, "id"), stringField(p, "version"), stringField(p, "description"))
		}
	}
	return nil
}

func runPluginsInfo(args []string) error {
	fs := flag.NewFlagSet("plugins info", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
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
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
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
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
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
