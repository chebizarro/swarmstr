package main

import (
	"bytes"
	"strings"
	"testing"
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
