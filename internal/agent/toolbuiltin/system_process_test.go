package toolbuiltin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ─── outputRing tests ─────────────────────────────────────────────────────────

func TestOutputRing_Write(t *testing.T) {
	r := newOutputRing(16)
	n, err := r.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("wrote %d, want 5", n)
	}
}

func TestOutputRing_ReadNew_Basic(t *testing.T) {
	r := newOutputRing(64)
	r.Write([]byte("hello world"))
	got := r.ReadNew()
	if got != "hello world" {
		t.Fatalf("ReadNew = %q, want 'hello world'", got)
	}
	// Second read should be empty.
	got2 := r.ReadNew()
	if got2 != "" {
		t.Fatalf("second ReadNew = %q, want empty", got2)
	}
}

func TestOutputRing_ReadNew_Incremental(t *testing.T) {
	r := newOutputRing(64)
	r.Write([]byte("first"))
	r.ReadNew() // consume
	r.Write([]byte("second"))
	got := r.ReadNew()
	if got != "second" {
		t.Fatalf("ReadNew = %q, want 'second'", got)
	}
}

func TestOutputRing_ReadNew_Wrap(t *testing.T) {
	r := newOutputRing(8) // small buffer
	r.Write([]byte("12345678"))
	r.ReadNew() // consume all
	r.Write([]byte("ABCDEF")) // wraps around
	got := r.ReadNew()
	if got != "ABCDEF" {
		t.Fatalf("ReadNew = %q, want 'ABCDEF'", got)
	}
}

func TestOutputRing_ReadNew_Truncated(t *testing.T) {
	r := newOutputRing(8)
	r.Write([]byte("1234"))
	r.ReadNew() // consume
	// Write more than buffer size without reading.
	r.Write([]byte("ABCDEFGHIJ")) // 10 bytes in 8-byte buffer
	got := r.ReadNew()
	if !strings.HasPrefix(got, "[...truncated...]") {
		t.Fatalf("expected truncation prefix, got %q", got)
	}
}

func TestOutputRing_ReadAll(t *testing.T) {
	r := newOutputRing(64)
	r.Write([]byte("hello"))
	got := r.ReadAll()
	if got != "hello" {
		t.Fatalf("ReadAll = %q, want 'hello'", got)
	}
	// ReadAll is not destructive for ReadNew.
	got2 := r.ReadNew()
	if got2 != "hello" {
		t.Fatalf("ReadNew after ReadAll = %q, want 'hello'", got2)
	}
}

func TestOutputRing_ReadAll_Empty(t *testing.T) {
	r := newOutputRing(64)
	if got := r.ReadAll(); got != "" {
		t.Fatalf("ReadAll on empty ring = %q, want empty", got)
	}
}

func TestOutputRing_ReadAll_Wrapped(t *testing.T) {
	r := newOutputRing(8)
	r.Write([]byte("12345678ABCD")) // 12 bytes in 8-byte buffer
	got := r.ReadAll()
	// Should contain the last 8 bytes.
	if got != "5678ABCD" {
		t.Fatalf("ReadAll = %q, want '5678ABCD'", got)
	}
}

func TestOutputRing_ReadNew_EmptyWrite(t *testing.T) {
	r := newOutputRing(16)
	if got := r.ReadNew(); got != "" {
		t.Fatalf("ReadNew with no writes = %q", got)
	}
}

// ─── ProcessRegistry tests ──────���────────────────────────────────────────────

func TestProcessRegistry_SpawnAndRead(t *testing.T) {
	reg := NewProcessRegistry()
	defer reg.Shutdown()

	id, err := reg.spawn(context.Background(), "test-session", "echo hello", "")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty id")
	}

	// Wait for process to exit.
	time.Sleep(200 * time.Millisecond)

	result, err := reg.read(id)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	stdout := result["stdout"].(string)
	if !strings.Contains(stdout, "hello") {
		t.Errorf("stdout = %q, expected 'hello'", stdout)
	}
}

func TestProcessRegistry_SpawnAndKill(t *testing.T) {
	reg := NewProcessRegistry()
	defer reg.Shutdown()

	id, err := reg.spawn(context.Background(), "test-session", "sleep 60", "")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	result, err := reg.kill(id)
	if err != nil {
		t.Fatalf("kill: %v", err)
	}
	if result["killed"] != true {
		t.Error("expected killed=true")
	}

	// Should be removed from registry.
	items := reg.list()
	if len(items) != 0 {
		t.Errorf("expected empty registry after kill, got %d entries", len(items))
	}
}

func TestProcessRegistry_List(t *testing.T) {
	reg := NewProcessRegistry()
	defer reg.Shutdown()

	id1, _ := reg.spawn(context.Background(), "session-a", "sleep 60", "")
	id2, _ := reg.spawn(context.Background(), "session-b", "sleep 60", "")
	defer reg.kill(id1)
	defer reg.kill(id2)

	items := reg.list()
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestProcessRegistry_MaxProcesses(t *testing.T) {
	reg := NewProcessRegistry()
	defer reg.Shutdown()

	var ids []string
	for i := 0; i < maxActiveProcesses; i++ {
		id, err := reg.spawn(context.Background(), "session", "sleep 60", "")
		if err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	defer func() {
		for _, id := range ids {
			reg.kill(id)
		}
	}()

	// Should fail on maxActiveProcesses+1.
	_, err := reg.spawn(context.Background(), "session", "sleep 60", "")
	if err == nil {
		t.Fatal("expected error when max processes exceeded")
	}
	if !strings.Contains(err.Error(), "max") {
		t.Errorf("error = %q, expected mention of max", err.Error())
	}
}

func TestProcessRegistry_ReadUnknown(t *testing.T) {
	reg := NewProcessRegistry()
	defer reg.Shutdown()

	_, err := reg.read("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown handle")
	}
}

func TestProcessRegistry_KillUnknown(t *testing.T) {
	reg := NewProcessRegistry()
	defer reg.Shutdown()

	_, err := reg.kill("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown handle")
	}
}

func TestProcessRegistry_Send(t *testing.T) {
	reg := NewProcessRegistry()
	defer reg.Shutdown()

	id, err := reg.spawn(context.Background(), "session", "cat", "")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer reg.kill(id)

	err = reg.send(id, "hello from stdin\n")
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	result, _ := reg.read(id)
	stdout := result["stdout"].(string)
	if !strings.Contains(stdout, "hello from stdin") {
		t.Errorf("stdout = %q, expected echo of input", stdout)
	}
}

func TestProcessRegistry_SendUnknown(t *testing.T) {
	reg := NewProcessRegistry()
	defer reg.Shutdown()

	err := reg.send("nonexistent", "hello")
	if err == nil {
		t.Fatal("expected error for unknown handle")
	}
}

func TestProcessRegistry_SendToExited(t *testing.T) {
	reg := NewProcessRegistry()
	defer reg.Shutdown()

	id, err := reg.spawn(context.Background(), "session", "true", "")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	// Wait for exit.
	time.Sleep(200 * time.Millisecond)

	err = reg.send(id, "hello")
	if err == nil {
		t.Fatal("expected error sending to exited process")
	}
	if !strings.Contains(err.Error(), "exited") {
		t.Errorf("error = %q, expected mention of exited", err.Error())
	}
}

func TestProcessRegistry_CleanupSession(t *testing.T) {
	reg := NewProcessRegistry()
	defer reg.Shutdown()

	_, _ = reg.spawn(context.Background(), "session-a", "sleep 60", "")
	_, _ = reg.spawn(context.Background(), "session-b", "sleep 60", "")

	reg.CleanupSession("session-a")

	items := reg.list()
	if len(items) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(items))
	}
	if items[0]["session_id"] != "session-b" {
		t.Errorf("remaining process session = %v, want session-b", items[0]["session_id"])
	}

	reg.CleanupSession("session-b")
}

func TestProcessRegistry_ExitCode(t *testing.T) {
	reg := NewProcessRegistry()
	defer reg.Shutdown()

	id, err := reg.spawn(context.Background(), "session", "exit 42", "")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	// Wait for exit.
	time.Sleep(200 * time.Millisecond)

	result, err := reg.read(id)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if result["running"] != false {
		t.Error("expected running=false")
	}
	if result["exit_code"].(int) != 42 {
		t.Errorf("exit_code = %v, want 42", result["exit_code"])
	}
}

// ─── ProcessExecTool tests ───────────────────────────────────────────────────

func TestProcessExecTool_Basic(t *testing.T) {
	out, err := ProcessExecTool(context.Background(), map[string]any{
		"command": "echo hello world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	json.Unmarshal([]byte(out), &result)
	if result["stdout"] != "hello world" {
		t.Errorf("stdout = %q, want 'hello world'", result["stdout"])
	}
	if result["exit_code"].(float64) != 0 {
		t.Errorf("exit_code = %v, want 0", result["exit_code"])
	}
}

func TestProcessExecTool_NonZeroExit(t *testing.T) {
	out, err := ProcessExecTool(context.Background(), map[string]any{
		"command": "exit 1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	json.Unmarshal([]byte(out), &result)
	if result["exit_code"].(float64) != 1 {
		t.Errorf("exit_code = %v, want 1", result["exit_code"])
	}
}

func TestProcessExecTool_Stderr(t *testing.T) {
	out, err := ProcessExecTool(context.Background(), map[string]any{
		"command": "echo oops >&2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	json.Unmarshal([]byte(out), &result)
	if !strings.Contains(result["stderr"].(string), "oops") {
		t.Errorf("stderr = %q, expected 'oops'", result["stderr"])
	}
}

func TestProcessExecTool_Directory(t *testing.T) {
	out, err := ProcessExecTool(context.Background(), map[string]any{
		"command":   "pwd",
		"directory": "/tmp",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	json.Unmarshal([]byte(out), &result)
	stdout := result["stdout"].(string)
	if !strings.Contains(stdout, "tmp") {
		t.Errorf("stdout = %q, expected /tmp path", stdout)
	}
}

func TestProcessExecTool_MissingCommand(t *testing.T) {
	_, err := ProcessExecTool(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestProcessExecTool_Timeout(t *testing.T) {
	out, err := ProcessExecTool(context.Background(), map[string]any{
		"command":         "sleep 60",
		"timeout_seconds": float64(1),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	json.Unmarshal([]byte(out), &result)
	if result["timed_out"] != true {
		t.Error("expected timed_out=true")
	}
}

func TestProcessExecTool_DurationReported(t *testing.T) {
	out, err := ProcessExecTool(context.Background(), map[string]any{
		"command": "true",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	json.Unmarshal([]byte(out), &result)
	if _, ok := result["duration_ms"]; !ok {
		t.Error("expected duration_ms field")
	}
}

// ─── Tool function tests ──��──────────────────────────────────────────────────

func TestProcessSpawnTool_EmptyCommand(t *testing.T) {
	reg := NewProcessRegistry()
	defer reg.Shutdown()

	tool := ProcessSpawnTool(reg)
	_, err := tool(context.Background(), map[string]any{
		"command": "  ",
	})
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestProcessReadTool_MissingID(t *testing.T) {
	reg := NewProcessRegistry()
	defer reg.Shutdown()

	tool := ProcessReadTool(reg)
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestProcessSendTool_MissingID(t *testing.T) {
	reg := NewProcessRegistry()
	defer reg.Shutdown()

	tool := ProcessSendTool(reg)
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestProcessSendTool_MissingInput(t *testing.T) {
	reg := NewProcessRegistry()
	defer reg.Shutdown()

	tool := ProcessSendTool(reg)
	_, err := tool(context.Background(), map[string]any{
		"id": "proc_1",
	})
	if err == nil {
		t.Fatal("expected error for missing input")
	}
}

func TestProcessKillTool_MissingID(t *testing.T) {
	reg := NewProcessRegistry()
	defer reg.Shutdown()

	tool := ProcessKillTool(reg)
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestProcessListTool_Empty(t *testing.T) {
	reg := NewProcessRegistry()
	defer reg.Shutdown()

	tool := ProcessListTool(reg)
	out, err := tool(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"processes":[]`) {
		t.Errorf("expected empty list, got %q", out)
	}
}
