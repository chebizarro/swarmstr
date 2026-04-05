package main

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"testing"

	skillspkg "metiq/internal/skills"
)

func TestRunInstallSpecUsesConfiguredNodeManager(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") == "1" {
		os.Exit(0)
	}

	orig := execCommandContext
	t.Cleanup(func() { execCommandContext = orig })

	var gotName string
	var gotArgs []string
	execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		gotName = name
		gotArgs = append([]string(nil), args...)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestRunInstallSpecUsesConfiguredNodeManager", "--")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		return cmd
	}

	spec := skillspkg.InstallSpec{Kind: "node", Package: "@acme/tool"}
	_, _, code, err := runInstallSpec(context.Background(), spec, skillspkg.InstallPreferences{NodeManager: "pnpm"})
	if err != nil || code != 0 {
		t.Fatalf("runInstallSpec failed: code=%d err=%v", code, err)
	}
	if gotName != "pnpm" {
		t.Fatalf("expected pnpm executable, got %q", gotName)
	}
	if want := []string{"add", "-g", "@acme/tool"}; !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("unexpected pnpm args: got=%#v want=%#v", gotArgs, want)
	}

	_, _, code, err = runInstallSpec(context.Background(), spec, skillspkg.InstallPreferences{})
	if err != nil || code != 0 {
		t.Fatalf("runInstallSpec default npm failed: code=%d err=%v", code, err)
	}
	if gotName != "npm" {
		t.Fatalf("expected npm executable by default, got %q", gotName)
	}
	if want := []string{"install", "-g", "@acme/tool"}; !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("unexpected npm args: got=%#v want=%#v", gotArgs, want)
	}
}
