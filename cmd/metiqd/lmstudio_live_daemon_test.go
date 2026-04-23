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
	"testing"
	"time"
)

func TestLMStudioLive_DaemonHarness(t *testing.T) {
	if os.Getenv("LMSTUDIO_DAEMON_LIVE_TEST") == "" && os.Getenv("LMSTUDIO_LIVE_TEST") == "" {
		t.Skip("set LMSTUDIO_DAEMON_LIVE_TEST=1 to run the live daemon harness")
	}
	if !lmStudioReachable(t) {
		t.Skip("LM Studio not reachable on localhost:1234")
	}

	relay := newLocalNostrRelay(t)
	defer relay.Close()

	relayURL := relay.URL()
	if override := strings.TrimSpace(os.Getenv("METIQ_LIVE_RELAY_URL")); override != "" {
		relayURL = override
	}

	h := newLiveDaemonHarness(t, relayURL, liveTestModel())
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
		publishResult := h.runAgent(t, "live-nostr", fmt.Sprintf("Use my_identity to learn your pubkey, then use nostr_publish to publish a kind 1 note whose content is EXACTLY %q. Reply with just PUBLISHED.", note))
		if strings.TrimSpace(publishResult) != "PUBLISHED" {
			t.Fatalf("publish result = %q, want PUBLISHED", publishResult)
		}
		time.Sleep(2 * time.Second)
		fetchResult := h.runAgent(t, "live-nostr", "Use my_identity to get your pubkey, then use nostr_fetch with kinds [1], authors set to your pubkey, and limit 1. Reply with just the content of the most recent note.")
		if strings.TrimSpace(fetchResult) != note {
			t.Fatalf("fetch result = %q, want %q", fetchResult, note)
		}
	})
}

type liveDaemonHarness struct {
	t            *testing.T
	cmd          *exec.Cmd
	baseURL      string
	token        string
	logPath      string
	workspaceDir string
	configJSON   []byte
}

func newLiveDaemonHarness(t *testing.T, relayURL, model string) *liveDaemonHarness {
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
	configPath := filepath.Join(homeDir, ".metiq", "config.json")
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
	config := fmt.Sprintf(`{
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
	"control": {"require_auth": false},
  "acp": {"transport": "auto"},
  "session": {},
  "storage": {"encrypt": false},
  "heartbeat": {},
  "tts": {},
  "cron": {},
  "hooks": {},
  "timeouts": {},
  "extra": {"approvals": {"tools": ["bash_exec"]}}
}
`, relayURL, model, workspaceDir)
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
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
	cmd := exec.Command(binPath, "--bootstrap", bootstrapPath)
	cmd.Env = append(os.Environ(), "HOME="+homeDir)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		t.Fatalf("start metiqd: %v", err)
	}
	_ = logFile.Close()

	h := &liveDaemonHarness{
		t:            t,
		cmd:          cmd,
		baseURL:      "http://" + adminAddr,
		token:        token,
		logPath:      logPath,
		workspaceDir: workspaceDir,
		configJSON:   []byte(config),
	}
	h.waitForHealth(t)
	h.syncConfig(t)
	h.waitForAuthorizedControl(t)
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

func (h *liveDaemonHarness) syncConfig(t *testing.T) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, h.baseURL+"/config", bytes.NewReader(h.configJSON))
	if err != nil {
		t.Fatalf("build config sync request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sync config: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync config status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
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

func (h *liveDaemonHarness) waitAgent(t *testing.T, runID string) string {
	t.Helper()
	result := h.call(t, "agent.wait", map[string]any{"run_id": runID, "timeout_ms": 120000})
	status, _ := result["status"].(string)
	if status != "" && status != "completed" && status != "ok" {
		t.Fatalf("agent.wait status=%q result=%#v", status, result)
	}
	text, _ := result["result"].(string)
	if strings.TrimSpace(text) == "" {
		t.Fatalf("agent.wait empty result: %#v", result)
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
		candidate := h.runAgent(t, fmt.Sprintf("%s-%d", sessionID, i+1), message)
		last = candidate
		if accept(candidate) {
			return candidate
		}
	}
	return last
}

func (h *liveDaemonHarness) waitForApprovalLog(t *testing.T, runID string) string {
	t.Helper()
	pattern := regexp.MustCompile(`exec approval requested id=(\S+) tool=bash_exec`)
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
