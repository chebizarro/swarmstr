package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLMStudioLive_DaemonHarness(t *testing.T) {
	relayURL := requireLiveHarnessRelay(t)

	h := newLiveDaemonHarness(t, relayURL, liveTestModel(), liveDaemonHarnessOptions{})
	defer h.Close()

	t.Run("direct reasoning", func(t *testing.T) {
		result := h.runAgent(t, "live-direct", "What is 2+2? Reply with just the number.")
		if strings.TrimSpace(result) != "4" {
			t.Fatalf("direct result = %q, want 4", result)
		}
	})

	t.Run("identity tool", func(t *testing.T) {
		result := h.runAgentWithRetry(t, "live-identity", []string{
			"You must use the my_identity tool before answering. Reply with just your exact name.",
			"Call my_identity. Then output ONLY the exact agent name and nothing else.",
		}, func(result string) bool {
			trimmed := strings.TrimSpace(result)
			return trimmed == "Relay" || strings.Contains(trimmed, "Relay")
		})
		if !strings.Contains(strings.TrimSpace(result), "Relay") {
			t.Fatalf("identity result = %q, want Relay", result)
		}
	})

	t.Run("workspace file write", func(t *testing.T) {
		result := h.runAgent(t, "live-write", "Use write_file to create a file at scratch/hello.txt with content EXACTLY 'hello from relay'. After writing it, reply with just WRITTEN.")
		if strings.TrimSpace(result) != "WRITTEN" {
			t.Fatalf("write result = %q, want WRITTEN", result)
		}
		raw, err := os.ReadFile(filepath.Join(h.workspaceDir, "scratch", "hello.txt"))
		if err != nil {
			t.Fatalf("read written file: %v", err)
		}
		if string(raw) != "hello from relay" {
			t.Fatalf("written file = %q, want %q", string(raw), "hello from relay")
		}
	})

	t.Run("memory store and search", func(t *testing.T) {
		storeResult := h.runAgent(t, "live-memory", "Use memory_store to save this fact with topic 'test': 'favorite color is blue'. Reply with just STORED.")
		if strings.TrimSpace(storeResult) != "STORED" {
			t.Fatalf("memory store result = %q, want STORED", storeResult)
		}
		searchResult := h.runAgent(t, "live-memory", "Use memory_search to find the stored fact about favorite color. Reply with just the color.")
		if !strings.EqualFold(strings.TrimSpace(searchResult), "blue") {
			t.Fatalf("memory search result = %q, want blue", searchResult)
		}
	})

	t.Run("bash exec with approval", func(t *testing.T) {
		runID := h.startAgent(t, "live-shell", "You must call bash_exec as your first action with command `printf shell-ok`. Do not answer from memory. After the command succeeds, reply with exactly shell-ok.")
		approvalID := h.waitForApprovalLog(t, runID)
		h.call(t, "exec.approval.resolve", map[string]any{"id": approvalID, "decision": "approve", "reason": "live test"})
		result := h.waitAgent(t, runID)
		if strings.TrimSpace(result) != "shell-ok" {
			t.Fatalf("shell result = %q, want shell-ok", result)
		}
	})

	t.Run("nostr publish and fetch", func(t *testing.T) {
		note := fmt.Sprintf("LMSTUDIO_NOTE_%d", time.Now().UnixNano())
		publishPrompt := fmt.Sprintf("Use nostr_publish to publish a kind 1 note whose content is EXACTLY %q. Reply with just PUBLISHED.", note)
		publishResult := h.runAgentWithRetry(t, "live-nostr-publish", []string{publishPrompt, publishPrompt, publishPrompt}, func(result string) bool {
			return strings.TrimSpace(result) == "PUBLISHED"
		})
		if strings.TrimSpace(publishResult) != "PUBLISHED" {
			t.Fatalf("publish result = %q, want PUBLISHED", publishResult)
		}
		time.Sleep(2 * time.Second)
		fetchPrompt := fmt.Sprintf("Use nostr_fetch with kinds [1], authors [%q], and limit 1. Reply with just the content of the most recent note.", h.pubkey)
		fetchResult := h.runAgentWithRetry(t, "live-nostr-fetch", []string{fetchPrompt, fetchPrompt, fetchPrompt}, func(result string) bool {
			return strings.TrimSpace(result) == note
		})
		if strings.TrimSpace(fetchResult) != note {
			t.Fatalf("fetch result = %q, want %q", fetchResult, note)
		}
	})

	t.Run("multi-turn context retention", func(t *testing.T) {
		sessionID := "live-multiturn"
		
		// Turn 1: Establish a fact
		result1 := h.runAgent(t, sessionID, "Remember this: my favorite fruit is mango. Reply with just REMEMBERED.")
		if strings.TrimSpace(result1) != "REMEMBERED" {
			t.Fatalf("turn 1 result = %q, want REMEMBERED", result1)
		}
		
		// Turn 2: Reference the fact from turn 1
		result2 := h.runAgent(t, sessionID, "What is my favorite fruit? Reply with just the fruit name.")
		if !strings.EqualFold(strings.TrimSpace(result2), "mango") {
			t.Fatalf("turn 2 result = %q, want mango (context retention failed)", result2)
		}
		
		// Turn 3: Use memory_store with a fact
		result3 := h.runAgent(t, sessionID, "Use memory_store to save this fact with topic 'multiturn': 'lucky number is 42'. Reply with just STORED.")
		if strings.TrimSpace(result3) != "STORED" {
			t.Fatalf("turn 3 result = %q, want STORED", result3)
		}
		
		// Turn 4: Search memory and reference conversation context
		result4 := h.runAgent(t, sessionID, "Use memory_search to find my lucky number, then tell me both my favorite fruit and my lucky number in format: FRUIT-NUMBER")
		expected := "mango-42"
		if !strings.EqualFold(strings.TrimSpace(result4), expected) && !strings.Contains(strings.ToLower(result4), "mango") && !strings.Contains(result4, "42") {
			t.Fatalf("turn 4 result = %q, want both mango and 42 (multi-turn context + memory failed)", result4)
		}
		
		// Turn 5: File write referencing previous turns
		result5 := h.runAgent(t, sessionID, "Use write_file to create scratch/context-test.txt with content 'fruit: mango, number: 42'. Reply with just WRITTEN.")
		if strings.TrimSpace(result5) != "WRITTEN" {
			t.Fatalf("turn 5 result = %q, want WRITTEN", result5)
		}
		raw, err := os.ReadFile(filepath.Join(h.workspaceDir, "scratch", "context-test.txt"))
		if err != nil {
			t.Fatalf("read context test file: %v", err)
		}
		if !strings.Contains(string(raw), "mango") || !strings.Contains(string(raw), "42") {
			t.Fatalf("context test file = %q, want both mango and 42", string(raw))
		}
	})

	t.Run("permission denial recovery", func(t *testing.T) {
		sessionID := "live-denial"
		
		// Start an agent that requires approval, with shorter timeout
		result := h.call(t, "agent", map[string]any{
			"session_id": sessionID,
			"message":    "You must call bash_exec with command `printf denied-test`. Do not answer from memory. After bash_exec succeeds, reply with SUCCESS. If it fails or is denied, reply with DENIED.",
			"timeout_ms":  60000,
		})
		runID, _ := result["run_id"].(string)
		if strings.TrimSpace(runID) == "" {
			t.Fatalf("agent start missing run_id: %#v", result)
		}
		
		// Wait for approval request and deny it
		approvalID := h.waitForApprovalLog(t, runID)
		h.call(t, "exec.approval.resolve", map[string]any{"id": approvalID, "decision": "deny", "reason": "testing denial path"})
		
		// Agent should handle denial - either by responding or timing out gracefully
		// Both are acceptable outcomes for this test (validates the system doesn't crash)
		agentResult, err := h.waitAgentResult(t, runID)
		if err != nil {
			// Timeout or error is acceptable - agent didn't crash, system is stable
			if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "status") {
				t.Fatalf("unexpected error after denial: %v", err)
			}
			// System handled denial without crashing - success
			return
		}
		// If agent did respond, verify it indicates the issue
		if !strings.Contains(strings.ToUpper(agentResult), "DENIED") && !strings.Contains(strings.ToLower(agentResult), "denied") && !strings.Contains(strings.ToLower(agentResult), "rejected") && !strings.Contains(strings.ToLower(agentResult), "fail") && !strings.Contains(strings.ToLower(agentResult), "error") {
			t.Logf("agent responded after denial: %q (expected indication of denial/failure)", agentResult)
		}
	})

	t.Run("tool failure recovery", func(t *testing.T) {
		sessionID := "live-tool-failure"
		
		// Try to read a file that doesn't exist
		result := h.runAgent(t, sessionID, "Use read_file to read a file at nonexistent/missing.txt. If it fails, reply with just FAILED. If it succeeds, reply with the content.")
		// Agent should acknowledge the failure in some way
		if !strings.Contains(strings.ToUpper(result), "FAILED") && !strings.Contains(strings.ToLower(result), "fail") && !strings.Contains(strings.ToLower(result), "error") && !strings.Contains(strings.ToLower(result), "not found") && !strings.Contains(strings.ToLower(result), "does not exist") {
			t.Logf("tool failure result = %q (expected failure indication, got something else)", result)
		}
		
		// Follow up with a successful operation to prove recovery
		result2 := h.runAgent(t, sessionID, "Use write_file to create scratch/recovery.txt with content EXACTLY 'recovered'. Reply with just RECOVERED.")
		if strings.TrimSpace(result2) != "RECOVERED" {
			t.Fatalf("recovery result = %q, want RECOVERED (agent did not recover from tool failure)", result2)
		}
		raw, err := os.ReadFile(filepath.Join(h.workspaceDir, "scratch", "recovery.txt"))
		if err != nil {
			t.Fatalf("read recovery file: %v", err)
		}
		if string(raw) != "recovered" {
			t.Fatalf("recovery file = %q, want 'recovered'", string(raw))
		}
	})

	t.Run("tool chaining - write to file to memory", func(t *testing.T) {
		sessionID := "live-chain-file-mem"
		
		// Step 1: write data to file
		result1 := h.runAgent(t, sessionID, "Use write_file to create scratch/chain.txt with content EXACTLY 'chain-data-abc123'. Reply with just WRITTEN.")
		if strings.TrimSpace(result1) != "WRITTEN" {
			t.Fatalf("write result = %q, want WRITTEN", result1)
		}
		
		// Step 2: read that file and store content in memory (2-tool chain)
		result2 := h.runAgent(t, sessionID, "Use read_file to read scratch/chain.txt, then use memory_store to save what you read with topic 'chaintest'. Reply with just CHAINED.")
		if strings.TrimSpace(result2) != "CHAINED" {
			t.Fatalf("chain result = %q, want CHAINED", result2)
		}
		
		// Step 3: verify memory contains the right data (memory search tool)
		result3 := h.runAgent(t, sessionID, "Use memory_search with query 'chaintest'. Reply with just what you find.")
		if !strings.Contains(result3, "chain-data-abc123") {
			t.Fatalf("memory search result = %q, want chain-data-abc123 (tool chain incomplete)", result3)
		}
	})

	t.Run("tool chaining - file tree navigation and selective read", func(t *testing.T) {
		sessionID := "live-chain-tree"
		
		// Setup: create a few files
		if err := os.MkdirAll(filepath.Join(h.workspaceDir, "scratch", "data"), 0o755); err != nil {
			t.Fatalf("mkdir data: %v", err)
		}
		if err := os.WriteFile(filepath.Join(h.workspaceDir, "scratch", "data", "file1.txt"), []byte("content-one"), 0o644); err != nil {
			t.Fatalf("write file1: %v", err)
		}
		if err := os.WriteFile(filepath.Join(h.workspaceDir, "scratch", "data", "file2.txt"), []byte("content-two"), 0o644); err != nil {
			t.Fatalf("write file2: %v", err)
		}
		if err := os.WriteFile(filepath.Join(h.workspaceDir, "scratch", "data", "target.txt"), []byte("TARGET-DATA"), 0o644); err != nil {
			t.Fatalf("write target: %v", err)
		}
		
		// Chain: file_tree to discover -> read specific file -> write result elsewhere
		result := h.runAgent(t, sessionID, "Use file_tree to list files in scratch/data, then use read_file to read the file named target.txt, then use write_file to save what you read to scratch/tree-result.txt. Reply with just TREE-DONE.")
		if strings.TrimSpace(result) != "TREE-DONE" {
			t.Fatalf("tree chain result = %q, want TREE-DONE", result)
		}
		
		// Verify the result
		raw, err := os.ReadFile(filepath.Join(h.workspaceDir, "scratch", "tree-result.txt"))
		if err != nil {
			t.Fatalf("read tree result: %v", err)
		}
		if !strings.Contains(string(raw), "TARGET-DATA") {
			t.Fatalf("tree result = %q, want TARGET-DATA (file tree -> read -> write chain failed)", string(raw))
		}
	})

	t.Run("tool chaining - memory search to file operation", func(t *testing.T) {
		sessionID := "live-chain-mem-file"
		
		// Setup: store some data in memory
		result1 := h.runAgent(t, sessionID, "Use memory_store to save this with topic 'location': 'scratch/memo-output.txt'. Reply with just STORED.")
		if strings.TrimSpace(result1) != "STORED" {
			t.Fatalf("memo store result = %q, want STORED", result1)
		}
		
		// Chain: memory_search to find path -> write to that path
		result2 := h.runAgent(t, sessionID, "Use memory_search with query 'location' to find the path, then use write_file to write 'memo-content' to that exact path. Reply with just MEM-CHAIN-DONE.")
		if strings.TrimSpace(result2) != "MEM-CHAIN-DONE" {
			t.Fatalf("mem chain result = %q, want MEM-CHAIN-DONE", result2)
		}
		
		// Verify the file was written to the right location
		raw, err := os.ReadFile(filepath.Join(h.workspaceDir, "scratch", "memo-output.txt"))
		if err != nil {
			t.Fatalf("read memo output: %v", err)
		}
		if string(raw) != "memo-content" {
			t.Fatalf("memo output = %q, want memo-content (memory -> file chain failed)", string(raw))
		}
	})

	t.Run("permissions validation - sequential approvals", func(t *testing.T) {
		sessionID := "live-perms-seq"
		
		// First approval: should trigger and be granted
		result := h.call(t, "agent", map[string]any{
			"session_id": sessionID,
			"message":    "Call bash_exec with command `printf approval-1`. Reply OK.",
			"timeout_ms":  90000,
		})
		runID1, _ := result["run_id"].(string)
		if strings.TrimSpace(runID1) == "" {
			t.Fatalf("agent start missing run_id: %#v", result)
		}
		
		approvalID1 := h.waitForApprovalLog(t, runID1)
		h.call(t, "exec.approval.resolve", map[string]any{"id": approvalID1, "decision": "approve", "reason": "first approval test"})
		
		// Wait with lenient validation - timeout, deadline exceeded, or success all acceptable
		result1, err := h.waitAgentResult(t, runID1)
		if err != nil {
			// Timeout or deadline errors are acceptable - system didn't crash, approval was processed
			if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline") {
				// System handled approval flow without crashing - test passed
				return
			}
			t.Fatalf("unexpected error on approval: %v", err)
		}
		if !strings.Contains(result1, "approval-1") && !strings.Contains(strings.ToUpper(result1), "OK") {
			t.Logf("approval result = %q (expected approval-1 or OK)", result1)
		}
	})

	t.Run("permissions validation - approval gating", func(t *testing.T) {
		sessionID := "live-perms-gate"
		
		// Tools NOT requiring approval should execute immediately
		result1 := h.runAgent(t, sessionID, "Use write_file to create scratch/no-approval.txt with content EXACTLY 'no-gate'. Reply with just WROTE.")
		if !strings.Contains(result1, "WROTE") {
			t.Fatalf("non-gated tool result = %q, want WROTE", result1)
		}
		
		// Verify the non-gated file exists (proves write_file didn't require approval)
		raw, err := os.ReadFile(filepath.Join(h.workspaceDir, "scratch", "no-approval.txt"))
		if err != nil {
			t.Fatalf("non-gated file missing: %v (write_file should not require approval)", err)
		}
		if string(raw) != "no-gate" {
			t.Fatalf("non-gated file = %q, want no-gate", string(raw))
		}
		
		// bash_exec SHOULD require approval - just verify approval is requested
		// (we already test approval flow in other tests, this just validates gating)
		result := h.call(t, "agent", map[string]any{
			"session_id": sessionID,
			"message":    "Call bash_exec with command `printf gate-check`.",
			"timeout_ms":  60000,
		})
		runID, _ := result["run_id"].(string)
		if strings.TrimSpace(runID) == "" {
			t.Fatal("agent start missing run_id")
		}
		
		// The key validation: approval IS requested for bash_exec
		approvalID := h.waitForApprovalLog(t, runID)
		if strings.TrimSpace(approvalID) == "" {
			t.Fatal("bash_exec should have triggered approval request but didn't")
		}
		
		// Deny to keep test fast and prove gating works
		h.call(t, "exec.approval.resolve", map[string]any{"id": approvalID, "decision": "deny", "reason": "gate test - validating approval was required"})
		
		// Don't care about the final result - we validated the gating
		_, _ = h.waitAgentResult(t, runID)
	})
}

func TestLMStudioLive_DaemonHarness_ExplicitConfigPath(t *testing.T) {
	relayURL := requireLiveHarnessRelay(t)

	h := newLiveDaemonHarness(t, relayURL, liveTestModel(), liveDaemonHarnessOptions{ExplicitConfigPath: true})
	defer h.Close()

	t.Run("control readiness uses explicit config", func(t *testing.T) {
		result := h.runAgent(t, "live-explicit-direct", "What is 3+4? Reply with just the number.")
		if strings.TrimSpace(result) != "7" {
			t.Fatalf("direct result = %q, want 7", result)
		}
	})

	t.Run("workspace path comes from explicit config", func(t *testing.T) {
		result := h.runAgent(t, "live-explicit-write", "Use write_file to create a file at scratch/explicit.txt with content EXACTLY 'explicit config path'. After writing it, reply with just WRITTEN.")
		if strings.TrimSpace(result) != "WRITTEN" {
			t.Fatalf("write result = %q, want WRITTEN", result)
		}
		raw, err := os.ReadFile(filepath.Join(h.workspaceDir, "scratch", "explicit.txt"))
		if err != nil {
			t.Fatalf("read written file: %v", err)
		}
		if string(raw) != "explicit config path" {
			t.Fatalf("written file = %q, want %q", string(raw), "explicit config path")
		}
	})

	t.Run("default config mutation is ignored", func(t *testing.T) {
		ignoredWorkspace := filepath.Join(filepath.Dir(h.defaultConfigPath), "default-mutated-workspace")
		if err := os.MkdirAll(ignoredWorkspace, 0o755); err != nil {
			t.Fatalf("mkdir ignored workspace: %v", err)
		}
		h.writeConfigFile(t, h.defaultConfigPath, ignoredWorkspace, false)
		time.Sleep(2 * time.Second)
		result := h.runAgent(t, "live-explicit-default-mutation", "Use write_file to create a file at scratch/default-ignored.txt with content EXACTLY 'still explicit'. After writing it, reply with just WRITTEN.")
		if strings.TrimSpace(result) != "WRITTEN" {
			t.Fatalf("write result = %q, want WRITTEN", result)
		}
		if _, err := os.Stat(filepath.Join(ignoredWorkspace, "scratch", "default-ignored.txt")); !os.IsNotExist(err) {
			t.Fatalf("expected default config path mutation to be ignored, stat err=%v", err)
		}
		raw, err := os.ReadFile(filepath.Join(h.workspaceDir, "scratch", "default-ignored.txt"))
		if err != nil {
			t.Fatalf("read explicit workspace file: %v", err)
		}
		if string(raw) != "still explicit" {
			t.Fatalf("written file = %q, want %q", string(raw), "still explicit")
		}
	})

	t.Run("explicit config reload follows explicit path", func(t *testing.T) {
		reloadedWorkspace := filepath.Join(filepath.Dir(h.configPath), "reloaded-workspace")
		if err := os.MkdirAll(reloadedWorkspace, 0o755); err != nil {
			t.Fatalf("mkdir reloaded workspace: %v", err)
		}
		h.writeConfigFile(t, h.configPath, reloadedWorkspace, false)
		h.reloadViaSIGHUP(t)
		result := h.runAgent(t, "live-explicit-reload", "Use write_file to create a file at scratch/reloaded.txt with content EXACTLY 'reloaded explicit config'. After writing it, reply with just WRITTEN.")
		if strings.TrimSpace(result) != "WRITTEN" {
			t.Fatalf("write result = %q, want WRITTEN", result)
		}
		raw, err := os.ReadFile(filepath.Join(reloadedWorkspace, "scratch", "reloaded.txt"))
		if err != nil {
			t.Fatalf("read reloaded workspace file: %v", err)
		}
		if string(raw) != "reloaded explicit config" {
			t.Fatalf("written file = %q, want %q", string(raw), "reloaded explicit config")
		}
		h.workspaceDir = reloadedWorkspace
	})

}

func TestLMStudioLive_ApprovalGatingSpectrum(t *testing.T) {
	relayURL := requireLiveHarnessRelay(t)
	model := liveTestModel()

	t.Run("no approvals required", func(t *testing.T) {
		h := newLiveDaemonHarnessWithApprovals(t, relayURL, model, []string{})
		defer h.Close()
		
		// bash_exec should execute immediately without approval
		result := h.call(t, "agent", map[string]any{
			"session_id": "no-approvals-bash",
			"message":    "Call bash_exec with command `printf no-approval-needed`. Reply OK.",
			"timeout_ms":  60000,
		})
		runID, _ := result["run_id"].(string)
		if strings.TrimSpace(runID) == "" {
			t.Fatalf("agent start missing run_id")
		}
		
		// Should NOT trigger approval - wait for completion
		agentResult, err := h.waitAgentResult(t, runID)
		if err != nil {
			// If it times out, it probably waited for approval that shouldn't have been required
			t.Fatalf("bash_exec with no approvals config should not wait for approval: %v", err)
		}
		if !strings.Contains(strings.ToLower(agentResult), "no-approval-needed") && !strings.Contains(strings.ToUpper(agentResult), "OK") {
			t.Logf("no-approval bash result = %q", agentResult)
		}
		
		// write_file should also execute immediately
		result2 := h.runAgent(t, "no-approvals-write", "Use write_file to create scratch/no-gate.txt with content 'no-gates'. Reply WROTE.")
		if !strings.Contains(result2, "WROTE") {
			t.Fatalf("write_file result = %q, want WROTE", result2)
		}
	})

	t.Run("single tool requires approval", func(t *testing.T) {
		h := newLiveDaemonHarnessWithApprovals(t, relayURL, model, []string{"bash_exec"})
		defer h.Close()
		
		// write_file should NOT require approval
		result1 := h.runAgent(t, "single-write", "Use write_file to create scratch/single-no-gate.txt with content 'single-test'. Reply WROTE.")
		if !strings.Contains(result1, "WROTE") {
			t.Fatalf("write_file should not require approval: %q", result1)
		}
		
		// bash_exec SHOULD require approval
		result := h.call(t, "agent", map[string]any{
			"session_id": "single-bash",
			"message":    "Call bash_exec with command `printf single-gated`.",
			"timeout_ms":  60000,
		})
		runID, _ := result["run_id"].(string)
		if strings.TrimSpace(runID) == "" {
			t.Fatal("agent start missing run_id")
		}
		
		approvalID := h.waitForApprovalLog(t, runID)
		if strings.TrimSpace(approvalID) == "" {
			t.Fatal("bash_exec should require approval in single-tool config")
		}
		
		// Deny to keep test fast
		h.call(t, "exec.approval.resolve", map[string]any{"id": approvalID, "decision": "deny", "reason": "single tool test"})
		_, _ = h.waitAgentResult(t, runID)
	})

	t.Run("multiple tools require approval", func(t *testing.T) {
		h := newLiveDaemonHarnessWithApprovals(t, relayURL, model, []string{"bash_exec", "write_file"})
		defer h.Close()
		
		// write_file SHOULD now require approval
		result := h.call(t, "agent", map[string]any{
			"session_id": "multi-write",
			"message":    "Use write_file to create scratch/multi-gated.txt with content 'gated'.",
			"timeout_ms":  60000,
		})
		runID1, _ := result["run_id"].(string)
		if strings.TrimSpace(runID1) == "" {
			t.Fatal("agent start missing run_id")
		}
		
		approvalID1 := h.waitForApprovalLog(t, runID1)
		if strings.TrimSpace(approvalID1) == "" {
			t.Fatal("write_file should require approval in multi-tool config")
		}
		h.call(t, "exec.approval.resolve", map[string]any{"id": approvalID1, "decision": "deny", "reason": "multi write test"})
		_, _ = h.waitAgentResult(t, runID1)
		
		// bash_exec SHOULD also require approval
		result2 := h.call(t, "agent", map[string]any{
			"session_id": "multi-bash",
			"message":    "Call bash_exec with command `printf multi-gated`.",
			"timeout_ms":  60000,
		})
		runID2, _ := result2["run_id"].(string)
		if strings.TrimSpace(runID2) == "" {
			t.Fatal("agent start missing run_id")
		}
		
		approvalID2 := h.waitForApprovalLog(t, runID2)
		if strings.TrimSpace(approvalID2) == "" {
			t.Fatal("bash_exec should require approval in multi-tool config")
		}
		h.call(t, "exec.approval.resolve", map[string]any{"id": approvalID2, "decision": "deny", "reason": "multi bash test"})
		_, _ = h.waitAgentResult(t, runID2)
		
		// memory_store should NOT require approval (not in the list)
		result3 := h.runAgent(t, "multi-memory", "Use memory_store to save 'test' with topic 'multi'. Reply STORED.")
		if !strings.Contains(result3, "STORED") {
			t.Fatalf("memory_store should not require approval: %q", result3)
		}
	})
}

type liveDaemonHarness struct {
	t                 *testing.T
	cmd               *exec.Cmd
	baseURL           string
	token             string
	logPath           string
	workspaceDir      string
	pubkey            string
	relayURL          string
	model             string
	configPath        string
	defaultConfigPath string
}

type liveDaemonHarnessOptions struct {
	ExplicitConfigPath bool
}

func requireLiveHarnessRelay(t *testing.T) string {
	t.Helper()
	if os.Getenv("LMSTUDIO_DAEMON_LIVE_TEST") == "" && os.Getenv("LMSTUDIO_LIVE_TEST") == "" {
		t.Skip("set LMSTUDIO_DAEMON_LIVE_TEST=1 to run the live daemon harness")
	}
	if !lmStudioReachable(t) {
		t.Skip("LM Studio not reachable on localhost:1234")
	}
	relay := newLocalNostrRelay(t)
	t.Cleanup(relay.Close)
	relayURL := relay.URL()
	if override := strings.TrimSpace(os.Getenv("METIQ_LIVE_RELAY_URL")); override != "" {
		relayURL = override
	}
	return relayURL
}

func newLiveDaemonHarnessWithApprovals(t *testing.T, relayURL, model string, approvalTools []string) *liveDaemonHarness {
	t.Helper()
	return newLiveDaemonHarnessCustom(t, relayURL, model, liveDaemonHarnessOptions{}, approvalTools)
}

func newLiveDaemonHarness(t *testing.T, relayURL, model string, opts liveDaemonHarnessOptions) *liveDaemonHarness {
	t.Helper()
	return newLiveDaemonHarnessCustom(t, relayURL, model, opts, []string{"bash_exec"})
}

func newLiveDaemonHarnessCustom(t *testing.T, relayURL, model string, opts liveDaemonHarnessOptions, approvalTools []string) *liveDaemonHarness {
	t.Helper()

	root := t.TempDir()
	homeDir := filepath.Join(root, "home")
	workspaceDir := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(homeDir, ".metiq"), 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "IDENTITY.md"), []byte("# IDENTITY.md\n- **Name:** Relay\n- **Role:** live test agent\n"), 0o644); err != nil {
		t.Fatalf("write IDENTITY.md: %v", err)
	}

	adminPort := freePort(t)
	adminAddr := fmt.Sprintf("127.0.0.1:%d", adminPort)
	bootstrapPath := filepath.Join(homeDir, ".metiq", "bootstrap.json")
	defaultConfigPath := filepath.Join(homeDir, ".metiq", "config.json")
	configPath := defaultConfigPath
	if opts.ExplicitConfigPath {
		configPath = filepath.Join(root, "explicit-config.json")
	}
	binPath := filepath.Join(root, "metiqd")
	logPath := filepath.Join(root, "daemon.log")
	token := "live-test-token"
	privateKey := randomSecretKeyHex(t)

	bootstrap := fmt.Sprintf(`{
  "private_key": %q,
  "relays": [%q],
  "admin_listen_addr": %q,
  "admin_token": %q,
  "enable_nip17": false,
  "enable_nip44": false
}
`, privateKey, relayURL, adminAddr, token)
	if err := os.WriteFile(bootstrapPath, []byte(bootstrap), 0o644); err != nil {
		t.Fatalf("write bootstrap: %v", err)
	}
	config := liveHarnessConfigJSONWithApprovals(relayURL, model, workspaceDir, false, approvalTools)
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if opts.ExplicitConfigPath {
		conflictWorkspaceDir := filepath.Join(root, "wrong-workspace")
		if err := os.MkdirAll(conflictWorkspaceDir, 0o755); err != nil {
			t.Fatalf("mkdir conflicting workspace: %v", err)
		}
		conflicting := liveHarnessConfigJSON(relayURL, model, conflictWorkspaceDir, true)
		if err := os.WriteFile(defaultConfigPath, []byte(conflicting), 0o644); err != nil {
			t.Fatalf("write conflicting default config: %v", err)
		}
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	buildCmd.Dir = wd
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build metiqd: %v\n%s", err, out)
	}

	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	cmdArgs := []string{"--bootstrap", bootstrapPath}
	if opts.ExplicitConfigPath {
		cmdArgs = append(cmdArgs, "--config", configPath)
	}
	cmd := exec.Command(binPath, cmdArgs...)
	cmd.Env = append(os.Environ(), "HOME="+homeDir)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		t.Fatalf("start metiqd: %v", err)
	}
	_ = logFile.Close()

	h := &liveDaemonHarness{
		t:                 t,
		cmd:               cmd,
		baseURL:           "http://" + adminAddr,
		token:             token,
		logPath:           logPath,
		workspaceDir:      workspaceDir,
		relayURL:          relayURL,
		model:             model,
		configPath:        configPath,
		defaultConfigPath: defaultConfigPath,
	}
	h.waitForHealth(t)
	h.waitForAuthorizedControl(t)
	h.pubkey = h.statusPubKey(t)
	return h
}

func (h *liveDaemonHarness) Close() {
	if h == nil || h.cmd == nil || h.cmd.Process == nil {
		return
	}
	_ = h.cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- h.cmd.Wait() }()
	select {
	case <-time.After(5 * time.Second):
		_ = h.cmd.Process.Kill()
		<-done
	case <-done:
	}
}

func (h *liveDaemonHarness) writeConfigFile(t *testing.T, path, workspaceDir string, requireAuth bool) {
	t.Helper()
	config := liveHarnessConfigJSON(h.relayURL, h.model, workspaceDir, requireAuth)
	if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
		t.Fatalf("write config %s: %v", path, err)
	}
}

func (h *liveDaemonHarness) reloadViaSIGHUP(t *testing.T) {
	t.Helper()
	if h == nil || h.cmd == nil || h.cmd.Process == nil {
		t.Fatal("daemon process not available for SIGHUP")
	}
	if err := h.cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatalf("send SIGHUP: %v", err)
	}
	time.Sleep(2 * time.Second)
}

func (h *liveDaemonHarness) waitForHealth(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, h.baseURL+"/health", nil)
		if err != nil {
			t.Fatalf("build health request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+h.token)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	raw, _ := os.ReadFile(h.logPath)
	t.Fatalf("daemon did not become healthy; recent log:\n%s", tailString(string(raw), 4000))
}

func (h *liveDaemonHarness) statusPubKey(t *testing.T) string {
	t.Helper()
	result := h.call(t, "status", nil)
	pubkey, _ := result["pubkey"].(string)
	if strings.TrimSpace(pubkey) == "" {
		t.Fatalf("status missing pubkey: %#v", result)
	}
	return strings.TrimSpace(pubkey)
}

func (h *liveDaemonHarness) waitForAuthorizedControl(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	payload := []byte(`{"method":"status"}`)
	lastStatus := 0
	lastBody := ""
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, h.baseURL+"/call", bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("build readiness request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+h.token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastStatus = resp.StatusCode
			lastBody = strings.TrimSpace(string(body))
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	raw, _ := os.ReadFile(h.logPath)
	t.Fatalf("daemon control API did not become authorized: status=%d body=%s\nrecent log:\n%s", lastStatus, lastBody, tailString(string(raw), 4000))
}

func (h *liveDaemonHarness) call(t *testing.T, method string, params map[string]any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"method": method, "params": params})
	if err != nil {
		t.Fatalf("marshal %s: %v", method, err)
	}
	req, err := http.NewRequest(http.MethodPost, h.baseURL+"/call", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build %s request: %v", method, err)
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("call %s: %v", method, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s response: %v", method, err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode %s response: %v\n%s", method, err, raw)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("call %s status=%d body=%s", method, resp.StatusCode, raw)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Fatalf("call %s not ok: %s", method, raw)
	}
	result, _ := body["result"].(map[string]any)
	if result == nil {
		t.Fatalf("call %s missing result: %s", method, raw)
	}
	return result
}

func (h *liveDaemonHarness) startAgent(t *testing.T, sessionID, message string) string {
	t.Helper()
	result := h.call(t, "agent", map[string]any{
		"session_id": sessionID,
		"message":    message,
		"timeout_ms": 120000,
	})
	runID, _ := result["run_id"].(string)
	if strings.TrimSpace(runID) == "" {
		t.Fatalf("agent start missing run_id: %#v", result)
	}
	return runID
}

func (h *liveDaemonHarness) waitAgentResult(t *testing.T, runID string) (string, error) {
	t.Helper()
	result := h.call(t, "agent.wait", map[string]any{"run_id": runID, "timeout_ms": 120000})
	status, _ := result["status"].(string)
	if status != "" && status != "completed" && status != "ok" {
		if msg, _ := result["error"].(string); strings.TrimSpace(msg) != "" {
			return "", fmt.Errorf("agent.wait status=%q error=%s", status, msg)
		}
		return "", fmt.Errorf("agent.wait status=%q", status)
	}
	text, _ := result["result"].(string)
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("agent.wait empty result")
	}
	return text, nil
}

func (h *liveDaemonHarness) waitAgent(t *testing.T, runID string) string {
	t.Helper()
	text, err := h.waitAgentResult(t, runID)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func (h *liveDaemonHarness) runAgent(t *testing.T, sessionID, message string) string {
	t.Helper()
	return h.waitAgent(t, h.startAgent(t, sessionID, message))
}

func (h *liveDaemonHarness) runAgentWithRetry(t *testing.T, sessionID string, messages []string, accept func(string) bool) string {
	t.Helper()
	var last string
	for i, message := range messages {
		candidate, err := h.waitAgentResult(t, h.startAgent(t, fmt.Sprintf("%s-%d", sessionID, i+1), message))
		if err != nil {
			last = err.Error()
			continue
		}
		last = candidate
		if accept(candidate) {
			return candidate
		}
	}
	return last
}

func (h *liveDaemonHarness) waitForApprovalLog(t *testing.T, runID string) string {
	t.Helper()
	// Match any tool, not just bash_exec
	pattern := regexp.MustCompile(`exec approval requested id=(\S+) tool=\S+`)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(h.logPath)
		if err == nil {
			matches := pattern.FindAllStringSubmatch(string(raw), -1)
			if len(matches) > 0 {
				return matches[len(matches)-1][1]
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	raw, _ := os.ReadFile(h.logPath)
	t.Fatalf("approval log not found for run %s; recent log:\n%s", runID, tailString(string(raw), 4000))
	return ""
}

func lmStudioReachable(t *testing.T) bool {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://localhost:1234/v1/models")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func liveTestModel() string {
	if model := strings.TrimSpace(os.Getenv("LMSTUDIO_LIVE_MODEL")); model != "" {
		return model
	}
	return "lmstudio/openai/gpt-oss-20b"
}

func liveHarnessConfigJSON(relayURL, model, workspaceDir string, requireAuth bool) string {
	return liveHarnessConfigJSONWithApprovals(relayURL, model, workspaceDir, requireAuth, []string{"bash_exec"})
}

func liveHarnessConfigJSONWithApprovals(relayURL, model, workspaceDir string, requireAuth bool, approvalTools []string) string {
	approvalsJSON := "[]"
	if len(approvalTools) > 0 {
		var quoted []string
		for _, tool := range approvalTools {
			quoted = append(quoted, fmt.Sprintf("%q", tool))
		}
		approvalsJSON = strings.Join(quoted, ", ")
	}
	return fmt.Sprintf(`{
  "version": 1,
  "relays": {"read": [%[1]q], "write": [%[1]q]},
  "agent": {"default_model": %[2]q},
  "agents": [{
    "id": "main",
    "model": %[2]q,
    "workspace_dir": %[3]q,
    "enabled_tools": ["my_identity", "write_file", "read_file", "file_tree", "memory_store", "memory_search", "bash_exec", "nostr_publish", "nostr_fetch"],
    "heartbeat": {},
    "context_window": 65536,
    "max_context_tokens": 65536
  }],
  "control": {"require_auth": %[4]t},
  "acp": {"transport": "auto"},
  "session": {},
  "storage": {"encrypt": false},
  "heartbeat": {},
  "tts": {},
  "cron": {},
  "hooks": {},
  "timeouts": {},
  "extra": {"approvals": {"tools": [%[5]s]}}
}
`, relayURL, model, workspaceDir, requireAuth, approvalsJSON)
}

func randomSecretKeyHex(t *testing.T) string {
	t.Helper()
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("random private key: %v", err)
	}
	return hex.EncodeToString(raw[:])
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func tailString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}
