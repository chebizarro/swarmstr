package commandanalysis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeExecutable(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExecutionBindingCanonicalizesAndRevalidates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable mode test")
	}
	dir := t.TempDir()
	exe := writeExecutable(t, dir, "runner", "#!/bin/sh\necho ok\n")
	link := filepath.Join(t.TempDir(), "cwd-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	req := ExecutionRequest{Argv: []string{exe, "arg one"}, CWD: link, Env: map[string]string{"BINDING_TEST": "one"}}
	binding, err := CaptureExecutionBinding(req)
	if err != nil {
		t.Fatal(err)
	}
	canonicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	canonicalExe, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	if binding.CanonicalCWD != canonicalDir || len(binding.Argv) != 2 || binding.Argv[0] != exe || binding.Executable.CanonicalPath != canonicalExe {
		t.Fatalf("binding=%+v", binding)
	}
	if strings.Contains(binding.SanitizedEnvSHA256, "one") {
		t.Fatal("environment value leaked")
	}
	if err := RevalidateExecutionBinding(binding, req); err != nil {
		t.Fatal(err)
	}

	changed := req
	changed.Argv = []string{exe, "different"}
	if err := RevalidateExecutionBinding(binding, changed); err == nil {
		t.Fatal("argv drift was accepted")
	}
	changed = req
	changed.Env = map[string]string{"BINDING_TEST": "two"}
	if err := RevalidateExecutionBinding(binding, changed); err == nil {
		t.Fatal("environment drift was accepted")
	}
}

func TestExecutionBindingPinsShellCommandExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable mode test")
	}
	dir := t.TempDir()
	exe := writeExecutable(t, dir, "runner", "#!/bin/sh\necho one\n")
	req := ExecutionRequest{CommandText: "runner argument", CWD: dir, Env: map[string]string{"PATH": dir}}
	binding, err := CaptureExecutionBinding(req)
	if err != nil {
		t.Fatal(err)
	}
	canonicalExe, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	if binding.CommandExecutable == nil || binding.CommandExecutable.CanonicalPath != canonicalExe {
		t.Fatalf("shell command executable was not pinned: %+v", binding)
	}
	writeExecutable(t, dir, "runner", "#!/bin/sh\necho replacement-content\n")
	if err := RevalidateExecutionBinding(binding, req); err == nil {
		t.Fatal("shell command executable replacement was accepted")
	}
}

func TestExecutionBindingDetectsExecutableReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable mode test")
	}
	dir := t.TempDir()
	exe := writeExecutable(t, dir, "runner", "#!/bin/sh\necho one\n")
	req := ExecutionRequest{Argv: []string{exe}, CWD: dir}
	binding, err := CaptureExecutionBinding(req)
	if err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, dir, "runner", "#!/bin/sh\necho replacement-content\n")
	if err := RevalidateExecutionBinding(binding, req); err == nil {
		t.Fatal("executable replacement was accepted")
	}
}

func TestExecutionBindingPinsInterpreterScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "job.sh")
	if err := os.WriteFile(script, []byte("echo one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	req := ExecutionRequest{CommandText: "sh job.sh", CWD: dir}
	binding, err := CaptureExecutionBinding(req)
	if err != nil {
		t.Fatal(err)
	}
	if binding.ScriptOperand == nil || binding.ScriptOperand.Operand != "job.sh" {
		t.Fatalf("binding=%+v", binding)
	}
	if err := os.WriteFile(script, []byte("echo changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RevalidateExecutionBinding(binding, req); err == nil {
		t.Fatal("script replacement was accepted")
	}
}

func TestExecutionBindingInlineInterpreterHasNoFileOperand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test")
	}
	binding, err := CaptureExecutionBinding(ExecutionRequest{CommandText: "sh -c 'echo hi'", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if binding.ScriptOperand != nil {
		t.Fatalf("unexpected script operand: %+v", binding.ScriptOperand)
	}
}

func TestExecutionBindingBindsSecretEnvironmentWithoutPersistingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable mode test")
	}
	dir := t.TempDir()
	exe := writeExecutable(t, dir, "runner", "#!/bin/sh\ntrue\n")
	one, err := CaptureExecutionBinding(ExecutionRequest{Argv: []string{exe}, CWD: dir, Env: map[string]string{"API_TOKEN": "one"}})
	if err != nil {
		t.Fatal(err)
	}
	two, err := CaptureExecutionBinding(ExecutionRequest{Argv: []string{exe}, CWD: dir, Env: map[string]string{"API_TOKEN": "two"}})
	if err != nil {
		t.Fatal(err)
	}
	if one.SanitizedEnvSHA256 == two.SanitizedEnvSHA256 {
		t.Fatal("secret environment drift did not change the sanitized binding digest")
	}
	encoded, err := json.Marshal(one)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "API_TOKEN") || strings.Contains(string(encoded), "one") {
		t.Fatalf("secret environment leaked into binding: %s", encoded)
	}
}
