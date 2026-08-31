package main

import "testing"

func TestApprovalExecutionRequestShellUsesExactCommand(t *testing.T) {
	req, bindable, err := approvalExecutionRequest("bash_exec", map[string]any{"command": "printf '%s' hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !bindable || req.CommandText != "printf '%s' hello" || len(req.Argv) != 0 {
		t.Fatalf("req=%+v bindable=%v", req, bindable)
	}
}

func TestApprovalExecutionRequestPreservesExactArgvAndEnv(t *testing.T) {
	req, bindable, err := approvalExecutionRequest("exec", map[string]any{
		"command_argv": []any{"printf", "a b"},
		"cwd":          ".",
		"env":          map[string]any{"LANG": "C"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bindable || len(req.Argv) != 2 || req.Argv[1] != "a b" || req.Env["LANG"] != "C" {
		t.Fatalf("req=%+v", req)
	}
}

func TestApprovalExecutionRequestDoesNotInventStructuredToolArgv(t *testing.T) {
	_, bindable, err := approvalExecutionRequest("git_status", map[string]any{"directory": "."})
	if err != nil {
		t.Fatal(err)
	}
	if bindable {
		t.Fatal("structured multi-command helper must not receive invented argv")
	}
}
