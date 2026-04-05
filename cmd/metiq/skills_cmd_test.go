package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestRunSkillsInfoUsesStatusAndAgentFilter(t *testing.T) {
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
				"skills": []map[string]any{{
					"id":                "verify",
					"name":              "verify",
					"status":            "ready",
					"source":            "metiq-bundled",
					"description":       "Verify results before replying",
					"whenToUse":         "Before finalizing risky changes",
					"primaryEnv":        "VERIFY_TOKEN",
					"selectedInstallId": "brew",
					"filePath":          "/tmp/skills/verify/SKILL.md",
				}},
			},
		})
	}))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	oldAddr := os.Getenv("METIQ_ADMIN_ADDR")
	defer os.Setenv("METIQ_ADMIN_ADDR", oldAddr)
	_ = os.Setenv("METIQ_ADMIN_ADDR", addr)

	out, err := captureStdout(t, func() error {
		return runSkillsInfo([]string{"--agent", "coder", "verify"})
	})
	if err != nil {
		t.Fatalf("runSkillsInfo: %v", err)
	}
	if gotMethod != "skills.status" {
		t.Fatalf("unexpected method: %s", gotMethod)
	}
	if gotParams["agent_id"] != "coder" {
		t.Fatalf("expected agent_id filter, got %#v", gotParams)
	}
	if !strings.Contains(out, "id: verify") || !strings.Contains(out, "whenToUse: Before finalizing risky changes") || !strings.Contains(out, "selectedInstallId: brew") {
		t.Fatalf("unexpected info output: %s", out)
	}
}

func TestRunSkillsCheckReportsUnhealthySkill(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/call" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"skills": []map[string]any{{
					"id":     "debug",
					"status": "missing_requirements",
					"missing": map[string]any{
						"bins":    []string{},
						"anyBins": []string{},
						"env":     []string{"DEBUG_TOKEN"},
						"os":      []string{},
						"config":  []string{},
					},
				}},
			},
		})
	}))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	oldAddr := os.Getenv("METIQ_ADMIN_ADDR")
	defer os.Setenv("METIQ_ADMIN_ADDR", oldAddr)
	_ = os.Setenv("METIQ_ADMIN_ADDR", addr)

	out, err := captureStdout(t, func() error {
		return runSkillsCheck([]string{"debug"})
	})
	if err == nil {
		t.Fatal("expected runSkillsCheck to fail for missing requirements")
	}
	if !strings.Contains(err.Error(), "1 skill checks failed") {
		t.Fatalf("unexpected check error: %v", err)
	}
	if !strings.Contains(out, "debug") || !strings.Contains(out, "missing_requirements") || !strings.Contains(out, "env: DEBUG_TOKEN") {
		t.Fatalf("unexpected check output: %s", out)
	}
}

func TestRunSkillsEnableDisableCallsSkillsUpdate(t *testing.T) {
	cases := []struct {
		name        string
		run         func([]string) error
		wantEnabled bool
		wantOutput  string
	}{
		{name: "enable", run: runSkillsEnable, wantEnabled: true, wantOutput: "Enabled verify"},
		{name: "disable", run: runSkillsDisable, wantEnabled: false, wantOutput: "Disabled verify"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
					"ok":     true,
					"result": map[string]any{"skillKey": "verify"},
				})
			}))
			defer ts.Close()

			addr := strings.TrimPrefix(ts.URL, "http://")
			oldAddr := os.Getenv("METIQ_ADMIN_ADDR")
			defer os.Setenv("METIQ_ADMIN_ADDR", oldAddr)
			_ = os.Setenv("METIQ_ADMIN_ADDR", addr)

			out, err := captureStdout(t, func() error {
				return tc.run([]string{"--agent", "coder", "verify"})
			})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if gotMethod != "skills.update" {
				t.Fatalf("unexpected method: %s", gotMethod)
			}
			if gotParams["skill_key"] != "verify" || gotParams["enabled"] != tc.wantEnabled || gotParams["agent_id"] != "coder" {
				t.Fatalf("unexpected params: %#v", gotParams)
			}
			if !strings.Contains(out, tc.wantOutput) {
				t.Fatalf("unexpected output: %s", out)
			}
		})
	}
}

func TestUsageIncludesSkillsCommands(t *testing.T) {
	out, err := captureStdout(t, func() error {
		usage()
		return nil
	})
	if err != nil {
		t.Fatalf("usage output failed: %v", err)
	}
	for _, needle := range []string{"skills check", "skills info", "skills enable", "skills disable"} {
		if !strings.Contains(out, needle) {
			t.Fatalf("usage missing %q: %s", needle, out)
		}
	}
}
