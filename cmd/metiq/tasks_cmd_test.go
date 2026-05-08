package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestRunTasksResumeCallsGateway(t *testing.T) {
	var gotMethod string
	var gotParams map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/call" {
			http.NotFound(w, r)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotMethod, _ = req["method"].(string)
		gotParams, _ = req["params"].(map[string]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"task": map[string]any{
					"task_id": "task-1",
					"title":   "T",
					"status":  "ready",
				},
			},
		})
	}))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	oldAddr := os.Getenv("METIQ_ADMIN_ADDR")
	defer os.Setenv("METIQ_ADMIN_ADDR", oldAddr)
	_ = os.Setenv("METIQ_ADMIN_ADDR", addr)

	out, err := captureStdout(t, func() error {
		return runTasks([]string{"resume", "--decision", "approved", "--reason", "looks good", "task-1"})
	})
	if err != nil {
		t.Fatalf("runTasks resume: %v", err)
	}
	if gotMethod != "tasks.resume" {
		t.Fatalf("unexpected method: %s", gotMethod)
	}
	if gotParams["task_id"] != "task-1" {
		t.Fatalf("unexpected task_id: %#v", gotParams["task_id"])
	}
	if gotParams["decision"] != "approved" {
		t.Fatalf("unexpected decision: %#v", gotParams["decision"])
	}
	if !strings.Contains(out, "task task-1 resumed (approved) -> status=ready") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestRunTasksListRejectsMissingTasksPayload(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
	}))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	oldAddr := os.Getenv("METIQ_ADMIN_ADDR")
	defer os.Setenv("METIQ_ADMIN_ADDR", oldAddr)
	_ = os.Setenv("METIQ_ADMIN_ADDR", addr)

	_, err := captureStdout(t, func() error { return runTasks([]string{"list"}) })
	if err == nil || !strings.Contains(err.Error(), "response missing tasks") {
		t.Fatalf("expected missing tasks error, got %v", err)
	}
}

func TestDecodeTaskPayloadsReturnErrors(t *testing.T) {
	if _, err := decodeTaskSpec(nil); err == nil {
		t.Fatal("expected nil task decode error")
	}
	if _, err := decodeTaskSpecList(map[string]any{}); err == nil || !strings.Contains(err.Error(), "must be an array") {
		t.Fatalf("expected task list type error, got %v", err)
	}
	if _, err := decodeTaskRunList(map[string]any{}); err == nil || !strings.Contains(err.Error(), "must be an array") {
		t.Fatalf("expected run list type error, got %v", err)
	}
}

func TestRunTasksRunsWithTaskIDCallsGetDirectly(t *testing.T) {
	var methods []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		method, _ := req["method"].(string)
		methods = append(methods, method)
		if method != "tasks.get" {
			t.Fatalf("unexpected method %q", method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"runs": []map[string]any{{"run_id": "run-1", "task_id": "task-1", "status": "running", "started_at": 100, "ended_at": 160}},
			},
		})
	}))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	oldAddr := os.Getenv("METIQ_ADMIN_ADDR")
	defer os.Setenv("METIQ_ADMIN_ADDR", oldAddr)
	_ = os.Setenv("METIQ_ADMIN_ADDR", addr)

	out, err := captureStdout(t, func() error { return runTasks([]string{"runs", "--task", "task-1"}) })
	if err != nil {
		t.Fatalf("runTasks runs: %v", err)
	}
	if len(methods) != 1 || methods[0] != "tasks.get" {
		t.Fatalf("expected only tasks.get, got %v", methods)
	}
	if !strings.Contains(out, "1m0s") {
		t.Fatalf("expected safe duration output, got %s", out)
	}
}

func TestRunTasksListShowsApprovalAndVerificationColumns(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/call" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"tasks": []map[string]any{
					{
						"task_id":      "task-a",
						"title":        "Alpha",
						"status":       "awaiting_approval",
						"created_at":   1710000000,
						"verification": map[string]any{"checks": []map[string]any{{"id": "v1", "type": "lint"}}},
					},
					{
						"task_id":    "task-b",
						"title":      "Beta",
						"status":     "in_progress",
						"created_at": 1710000100,
						"meta": map[string]any{
							"approval_decision":   "approved",
							"verification_status": "passed",
						},
					},
				},
			},
		})
	}))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	oldAddr := os.Getenv("METIQ_ADMIN_ADDR")
	defer os.Setenv("METIQ_ADMIN_ADDR", oldAddr)
	_ = os.Setenv("METIQ_ADMIN_ADDR", addr)

	out, err := captureStdout(t, func() error {
		return runTasks([]string{"list"})
	})
	if err != nil {
		t.Fatalf("runTasks list: %v", err)
	}
	if !strings.Contains(out, "APPROVAL") || !strings.Contains(out, "VERIFICATION") {
		t.Fatalf("expected approval/verification columns, got: %s", out)
	}
	if !strings.Contains(out, "awaiting_approval") {
		t.Fatalf("expected awaiting approval state, got: %s", out)
	}
	if !strings.Contains(out, "passed") {
		t.Fatalf("expected verification status, got: %s", out)
	}
}
