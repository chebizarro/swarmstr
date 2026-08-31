package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fatih/color"
)

func TestCommandRegistryDispatchesAliases(t *testing.T) {
	called := false
	r := &commandRegistry{byName: map[string]*cliCommand{}}
	r.add(cliCommand{Name: "primary", Aliases: []string{"alias"}, Run: func(args []string) error {
		called = true
		if len(args) != 1 || args[0] != "arg" {
			t.Fatalf("unexpected args: %#v", args)
		}
		return nil
	}})

	handled, err := r.dispatch([]string{"alias", "arg"})
	if err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	if !handled || !called {
		t.Fatalf("expected alias dispatch to run handler")
	}
}

func TestParityAliasesResolveToCanonicalCommands(t *testing.T) {
	r := newCommandRegistry("")
	for alias, canonical := range map[string]string{
		"gateway":        "gw",
		"automations":    "cron",
		"exec-approvals": "approvals",
		"triage":         "diagnostics",
	} {
		got := r.byName[alias]
		if got == nil || got.Name != canonical {
			t.Errorf("alias %q resolved to %#v, want %q", alias, got, canonical)
		}
	}
}

func TestCommandRegistryConsumesGlobalFlags(t *testing.T) {
	oldJSON := cliGlobalJSON
	oldNoColor := cliNoColor
	oldColor := color.NoColor
	defer func() { cliGlobalJSON = oldJSON; cliNoColor = oldNoColor; color.NoColor = oldColor }()
	cliGlobalJSON = false
	cliNoColor = false

	var gotArgs []string
	r := &commandRegistry{byName: map[string]*cliCommand{}}
	r.add(cliCommand{Name: "primary", Run: func(args []string) error {
		gotArgs = append([]string(nil), args...)
		return nil
	}})

	handled, err := r.dispatch([]string{"--json", "primary", "--no-color", "arg"})
	if err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	if !handled || !cliGlobalJSON || !cliNoColor {
		t.Fatalf("expected global flags to be consumed and applied")
	}
	if len(gotArgs) != 1 || gotArgs[0] != "arg" {
		t.Fatalf("unexpected command args: %#v", gotArgs)
	}
}

func TestCompletionGeneratedFromRegistryIncludesSetupCommands(t *testing.T) {
	out := bashCompletion()
	for _, want := range []string{"setup", "onboard", "configure"} {
		if !strings.Contains(out, want) {
			t.Fatalf("completion missing %q: %s", want, out)
		}
	}
}

func TestInteractiveSetupUsesProvidedPath(t *testing.T) {
	var out bytes.Buffer
	if err := interactiveSetup(strings.NewReader(""), &out, "/tmp/metiq-config.json"); err != nil {
		t.Fatalf("interactive setup failed: %v", err)
	}
	if !strings.Contains(out.String(), "Using config path: /tmp/metiq-config.json") {
		t.Fatalf("unexpected setup output: %s", out.String())
	}
}
