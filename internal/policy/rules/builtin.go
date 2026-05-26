package rules

import (
	"embed"
	"fmt"
	"sort"
)

//go:embed builtin/*.local.md
var builtinFS embed.FS

func BuiltinRulePacks() ([]Rule, error) {
	entries, err := builtinFS.ReadDir("builtin")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	out := make([]Rule, 0, len(names))
	for _, name := range names {
		raw, err := builtinFS.ReadFile("builtin/" + name)
		if err != nil {
			return nil, err
		}
		r, err := ParseMarkdownRule(string(raw))
		if err != nil {
			return nil, fmt.Errorf("builtin %s: %w", name, err)
		}
		r.Source = "builtin/" + name
		out = append(out, r)
	}
	return out, nil
}
