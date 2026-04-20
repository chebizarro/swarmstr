package toolbuiltin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// ─── CurrentTimeTool tests ───────────────────────────────────────────────────

func TestCurrentTimeTool(t *testing.T) {
	out, err := CurrentTimeTool(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "UTC:") {
		t.Errorf("expected 'UTC:' in output, got %q", out)
	}
	if !strings.Contains(out, "Unix:") {
		t.Errorf("expected 'Unix:' in output, got %q", out)
	}
}

// ─── BashExecTool tests ─────────────────────────────────────────────────────

func TestBashExecTool_Basic(t *testing.T) {
	out, err := BashExecTool(context.Background(), map[string]any{
		"command": "echo hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result bashExecResult
	json.Unmarshal([]byte(out), &result)
	if result.Stdout != "hello" {
		t.Errorf("stdout = %q, want 'hello'", result.Stdout)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", result.ExitCode)
	}
}

func TestBashExecTool_NonZeroExit(t *testing.T) {
	out, err := BashExecTool(context.Background(), map[string]any{
		"command": "exit 2",
	})
	// BashExecTool returns both out and err for non-zero exit.
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	var result bashExecResult
	json.Unmarshal([]byte(out), &result)
	if result.ExitCode != 2 {
		t.Errorf("exit_code = %d, want 2", result.ExitCode)
	}
}

func TestBashExecTool_EmptyCommand(t *testing.T) {
	_, err := BashExecTool(context.Background(), map[string]any{
		"command": "  ",
	})
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestBashExecTool_Stderr(t *testing.T) {
	out, _ := BashExecTool(context.Background(), map[string]any{
		"command": "echo err >&2; exit 1",
	})
	var result bashExecResult
	json.Unmarshal([]byte(out), &result)
	if !strings.Contains(result.Stderr, "err") {
		t.Errorf("stderr = %q, expected 'err'", result.Stderr)
	}
}

func TestBashExecTool_Timeout(t *testing.T) {
	_, err := BashExecTool(context.Background(), map[string]any{
		"command":         "sleep 60",
		"timeout_seconds": float64(1),
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, expected 'timed out'", err.Error())
	}
}

func TestBashExecTool_Duration(t *testing.T) {
	out, err := BashExecTool(context.Background(), map[string]any{
		"command": "true",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result bashExecResult
	json.Unmarshal([]byte(out), &result)
	if result.DurationMs < 0 {
		t.Errorf("negative duration: %d", result.DurationMs)
	}
}
