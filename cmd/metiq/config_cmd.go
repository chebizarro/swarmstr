package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"metiq/internal/config"
	"metiq/internal/policy"
	"metiq/internal/store/state"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/tailscale/hujson"
)

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

	doc, err := loadConfigForCLI(configPath)
	if err != nil {
		return err
	}
	m, err := configDocMap(doc, false)
	if err != nil {
		return err
	}

	if key := fs.Arg(0); key != "" {
		cur, ok := getPath(m, key)
		if !ok {
			return fmt.Errorf("key %q not found", key)
		}
		if s, ok := cur.(string); ok && !jsonOut {
			fmt.Println(s)
			return nil
		}
		return printIndentedJSON(cur)
	}
	return printIndentedJSON(doc)
}

func runConfigList(args []string) error {
	fs := flag.NewFlagSet("config list", flag.ContinueOnError)
	var configPath string
	var redact bool
	fs.StringVar(&configPath, "path", "", "config file path")
	fs.BoolVar(&redact, "redact", true, "redact sensitive values")
	if err := fs.Parse(args); err != nil {
		return err
	}
	doc, err := loadConfigForCLI(configPath)
	if err != nil {
		return err
	}
	m, err := configDocMap(doc, redact)
	if err != nil {
		return err
	}
	prefix := fs.Arg(0)
	if prefix != "" {
		v, ok := getPath(m, prefix)
		if !ok {
			return fmt.Errorf("key %q not found", prefix)
		}
		m = map[string]any{prefix: v}
		prefix = ""
	}
	for _, line := range flattenConfig(m, prefix) {
		fmt.Println(line)
	}
	return nil
}

func runConfigSet(args []string) error {
	fs := flag.NewFlagSet("config set", flag.ContinueOnError)
	var configPath string
	var dryRun bool
	fs.StringVar(&configPath, "path", "", "config file path")
	fs.BoolVar(&dryRun, "dry-run", false, "validate and print result without writing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: config set [--path file] [--dry-run] <key> <json5-value>")
	}
	return mutateConfig(configPath, dryRun, func(m map[string]any) error {
		value, err := parseJSON5Value(fs.Arg(1))
		if err != nil {
			return err
		}
		return setPath(m, fs.Arg(0), value)
	})
}

func runConfigUnset(args []string) error {
	fs := flag.NewFlagSet("config unset", flag.ContinueOnError)
	var configPath string
	var dryRun bool
	fs.StringVar(&configPath, "path", "", "config file path")
	fs.BoolVar(&dryRun, "dry-run", false, "validate and print result without writing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: config unset [--path file] [--dry-run] <key>")
	}
	return mutateConfig(configPath, dryRun, func(m map[string]any) error {
		return unsetPath(m, fs.Arg(0))
	})
}

func runConfigPatch(args []string) error {
	fs := flag.NewFlagSet("config patch", flag.ContinueOnError)
	var configPath, patchFile string
	var dryRun bool
	fs.StringVar(&configPath, "path", "", "config file path")
	fs.StringVar(&patchFile, "file", "", "patch file (default: stdin)")
	fs.BoolVar(&dryRun, "dry-run", false, "validate and print result without writing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var raw []byte
	var err error
	if patchFile == "" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(patchFile)
	}
	if err != nil {
		return fmt.Errorf("read patch: %w", err)
	}
	patch, err := parseJSON5Object(raw)
	if err != nil {
		return err
	}
	return mutateConfig(configPath, dryRun, func(m map[string]any) error {
		mergeMap(m, patch)
		return nil
	})
}

func runConfigSchema(args []string) error {
	fs := flag.NewFlagSet("config schema", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	schema := map[string]any{
		"type":           "object",
		"description":    "Metiq configuration document. Values are validated by `metiq config validate`.",
		"commands":       []string{"config get <path>", "config set <path> <json5-value>", "config unset <path>", "config patch --file <file>", "config list"},
		"path_syntax":    "dot paths with bracket array indexes, e.g. relays.read[0] or agents[0].id",
		"top_level_keys": []string{"version", "dm", "relays", "agent", "control", "acp", "agents", "nostr_channels", "providers", "session", "storage", "heartbeat", "tts", "secrets", "cron", "hooks", "timeouts", "agent_list", "fips", "permissions", "extra"},
	}
	return printIndentedJSON(schema)
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
	if err := policy.ValidateConfig(policy.NormalizeConfig(doc)); err != nil {
		fmt.Fprintf(os.Stderr, "  %v\n", err)
		return fmt.Errorf("config has policy validation error")
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

func loadConfigForCLI(configPath string) (state.ConfigDoc, error) {
	if configPath == "" {
		var err error
		configPath, err = config.DefaultConfigPath()
		if err != nil {
			return state.ConfigDoc{}, fmt.Errorf("resolve default config path: %w", err)
		}
	}
	doc, err := config.LoadConfigFile(configPath)
	if err != nil {
		return state.ConfigDoc{}, fmt.Errorf("load config: %w", err)
	}
	return doc, nil
}

func configDocMap(doc state.ConfigDoc, redact bool) (map[string]any, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if redact {
		m = config.RedactMap(m)
	}
	return m, nil
}

func mutateConfig(configPath string, dryRun bool, mutate func(map[string]any) error) error {
	if configPath == "" {
		var err error
		configPath, err = config.DefaultConfigPath()
		if err != nil {
			return fmt.Errorf("resolve default config path: %w", err)
		}
	}
	configPath, err := config.ValidateConfigWritePath(configPath)
	if err != nil {
		return err
	}
	doc, err := config.LoadConfigFile(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	m, err := configDocMap(doc, false)
	if err != nil {
		return err
	}
	if err := mutate(m); err != nil {
		return err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	next, err := config.ParseConfigBytes(raw, configPath)
	if err != nil {
		return fmt.Errorf("parse mutated config: %w", err)
	}
	if errs := config.ValidateConfigDoc(next); len(errs) > 0 {
		return fmt.Errorf("validate config: %v", errs[0])
	}
	if err := policy.ValidateConfig(policy.NormalizeConfig(next)); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	if dryRun {
		redacted, err := configDocMap(next, true)
		if err != nil {
			return err
		}
		fmt.Printf("config valid — would write to %s\n", configPath)
		return printIndentedJSON(redacted)
	}
	if err := config.WriteConfigFile(configPath, next); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Printf("config updated → %s\n", configPath)
	return nil
}

func parseJSON5Value(s string) (any, error) {
	standardized, err := hujson.Standardize([]byte(s))
	if err == nil {
		var v any
		if err := json.Unmarshal(standardized, &v); err == nil {
			return v, nil
		}
	}
	if unquoted := strings.TrimSpace(s); unquoted != "" {
		return unquoted, nil
	}
	return nil, fmt.Errorf("value is empty")
}

func parseJSON5Object(raw []byte) (map[string]any, error) {
	standardized, err := hujson.Standardize(raw)
	if err != nil {
		return nil, fmt.Errorf("parse patch JSON5 object: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(standardized, &m); err != nil {
		return nil, fmt.Errorf("parse patch JSON object: %w", err)
	}
	if m == nil {
		return nil, fmt.Errorf("patch must be a JSON object")
	}
	return m, nil
}

type pathStep struct {
	key   string
	index *int
}

func parseConfigPath(path string) ([]pathStep, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("config key path is required")
	}
	var steps []pathStep
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			return nil, fmt.Errorf("invalid empty path segment in %q", path)
		}
		for part != "" {
			bracket := strings.Index(part, "[")
			if bracket < 0 {
				steps = append(steps, pathStep{key: part})
				break
			}
			if bracket > 0 {
				steps = append(steps, pathStep{key: part[:bracket]})
			}
			end := strings.Index(part[bracket:], "]")
			if end < 0 {
				return nil, fmt.Errorf("unclosed array index in %q", path)
			}
			idxText := part[bracket+1 : bracket+end]
			idx, err := strconv.Atoi(idxText)
			if err != nil || idx < 0 {
				return nil, fmt.Errorf("invalid array index %q", idxText)
			}
			steps = append(steps, pathStep{index: &idx})
			part = part[bracket+end+1:]
		}
	}
	return steps, nil
}

func getPath(root any, path string) (any, bool) {
	steps, err := parseConfigPath(path)
	if err != nil {
		return nil, false
	}
	cur := root
	for _, step := range steps {
		if step.index != nil {
			arr, ok := cur.([]any)
			if !ok || *step.index >= len(arr) {
				return nil, false
			}
			cur = arr[*step.index]
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[step.key]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func setPath(root map[string]any, path string, value any) error {
	steps, err := parseConfigPath(path)
	if err != nil {
		return err
	}
	return setPathValue(root, steps, value)
}

func setPathValue(cur any, steps []pathStep, value any) error {
	if len(steps) == 0 {
		return fmt.Errorf("config key path is required")
	}
	step := steps[0]
	last := len(steps) == 1
	if step.index != nil {
		arr, ok := cur.([]any)
		if !ok || *step.index >= len(arr) {
			return fmt.Errorf("array index %d not found", *step.index)
		}
		if last {
			arr[*step.index] = value
			return nil
		}
		return setPathValue(arr[*step.index], steps[1:], value)
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return fmt.Errorf("path segment %q is not an object", step.key)
	}
	if last {
		m[step.key] = value
		return nil
	}
	next, ok := m[step.key]
	if !ok {
		next = map[string]any{}
		m[step.key] = next
	}
	return setPathValue(next, steps[1:], value)
}

func unsetPath(root map[string]any, path string) error {
	steps, err := parseConfigPath(path)
	if err != nil {
		return err
	}
	if len(steps) == 0 {
		return fmt.Errorf("config key path is required")
	}
	return unsetPathValue(root, steps)
}

func unsetPathValue(cur any, steps []pathStep) error {
	step := steps[0]
	last := len(steps) == 1
	if step.index != nil {
		return fmt.Errorf("cannot unset array element by index")
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return fmt.Errorf("path segment %q is not an object", step.key)
	}
	if last {
		if _, ok := m[step.key]; !ok {
			return fmt.Errorf("key %q not found", step.key)
		}
		delete(m, step.key)
		return nil
	}
	next, ok := m[step.key]
	if !ok {
		return fmt.Errorf("key %q not found", step.key)
	}
	return unsetPathValue(next, steps[1:])
}

func mergeMap(dst, src map[string]any) {
	for k, v := range src {
		if srcMap, ok := v.(map[string]any); ok {
			if dstMap, ok := dst[k].(map[string]any); ok {
				mergeMap(dstMap, srcMap)
				continue
			}
		}
		dst[k] = v
	}
}

func flattenConfig(m map[string]any, prefix string) []string {
	var out []string
	var walk func(string, any)
	walk = func(path string, v any) {
		switch vv := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(vv))
			for k := range vv {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				child := k
				if path != "" {
					child = path + "." + k
				}
				walk(child, vv[k])
			}
		case []any:
			for i, item := range vv {
				walk(fmt.Sprintf("%s[%d]", path, i), item)
			}
		default:
			b, _ := json.Marshal(vv)
			out = append(out, fmt.Sprintf("%s = %s", path, b))
		}
	}
	walk(prefix, m)
	sort.Strings(out)
	return out
}

func printIndentedJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
