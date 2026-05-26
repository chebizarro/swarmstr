package main

import (
	"archive/zip"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func runDiagnostics(args []string) error {
	if len(args) == 0 {
		return runDiagnosticsExport(nil)
	}
	switch args[0] {
	case "export", "bundle":
		return runDiagnosticsExport(args[1:])
	default:
		return fmt.Errorf("diagnostics subcommands: export")
	}
}

func runDiagnosticsExport(args []string) error {
	fs := flag.NewFlagSet("diagnostics export", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, outPath string
	var jsonOut, includeLogs, includeRawConfig bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&outPath, "out", "", "diagnostic zip path")
	fs.BoolVar(&includeLogs, "include-logs", false, "include recent daemon logs in the bundle")
	fs.BoolVar(&includeRawConfig, "include-raw-config", false, "include unredacted config.get output")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if outPath == "" {
		outPath = fmt.Sprintf("metiq-diagnostics-%s.zip", time.Now().UTC().Format("20060102T150405Z"))
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	bundle := map[string]any{
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"version":    version,
		"methods":    map[string]any{},
	}
	methods := bundle["methods"].(map[string]any)
	requests := []struct {
		Name   string
		Params map[string]any
	}{
		{"status.get", map[string]any{"verbose": true}},
		{"config.get", map[string]any{}},
		{"plugins.registry.list", map[string]any{}},
		{"agents.list", map[string]any{}},
		{"channels.list", map[string]any{}},
		{"usage.status", map[string]any{}},
		{"usage.cost", map[string]any{}},
	}
	if includeLogs {
		requests = append(requests, struct {
			Name   string
			Params map[string]any
		}{"logs.tail", map[string]any{"lines": 200}})
	}
	for _, req := range requests {
		res, callErr := cl.call(req.Name, req.Params)
		if callErr != nil {
			methods[req.Name] = map[string]any{"ok": false, "error": callErr.Error()}
			continue
		}
		result := any(res)
		if req.Name == "config.get" && !includeRawConfig {
			result = redactDiagnosticsValue(res)
		}
		methods[req.Name] = map[string]any{"ok": true, "result": result}
	}
	files, err := createDiagnosticsZip(outPath, bundle)
	if err != nil {
		return err
	}
	result := map[string]any{"ok": true, "path": outPath, "files": files, "count": len(files)}
	if jsonOut {
		return printJSON(result)
	}
	printSuccess("diagnostics exported: %s", outPath)
	for _, name := range files {
		printMuted("  %s", name)
	}
	return nil
}

func redactDiagnosticsValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			if diagnosticSecretKey(key) {
				out[key] = "[REDACTED]"
				continue
			}
			out[key] = redactDiagnosticsValue(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = redactDiagnosticsValue(child)
		}
		return out
	default:
		return value
	}
}

func diagnosticSecretKey(key string) bool {
	k := strings.ToLower(key)
	for _, marker := range []string{"secret", "token", "password", "api_key", "apikey", "authorization", "bearer", "private", "credential"} {
		if strings.Contains(k, marker) {
			return true
		}
	}
	return false
}

func createDiagnosticsZip(outPath string, bundle map[string]any) ([]string, error) {
	if dir := filepath.Dir(outPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	zw := zip.NewWriter(out)
	written := []string{}
	success := false
	defer func() {
		if !success {
			_ = os.Remove(outPath)
		}
	}()
	writeJSON := func(name string, value any) error {
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := w.Write(raw); err != nil {
			return err
		}
		written = append(written, name)
		return nil
	}
	if err := writeJSON("metiq-diagnostics/manifest.json", map[string]any{"created_at": bundle["created_at"], "version": bundle["version"]}); err != nil {
		return nil, err
	}
	if err := writeJSON("metiq-diagnostics/bundle.json", bundle); err != nil {
		return nil, err
	}
	if methods, ok := bundle["methods"].(map[string]any); ok {
		for name, value := range methods {
			if err := writeJSON("metiq-diagnostics/methods/"+name+".json", value); err != nil {
				_ = zw.Close()
				_ = out.Close()
				return nil, err
			}
		}
	}
	if err := zw.Close(); err != nil {
		_ = out.Close()
		return nil, err
	}
	if err := out.Close(); err != nil {
		return nil, err
	}
	success = true
	return written, nil
}
