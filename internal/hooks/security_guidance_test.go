package hooks

import (
	"strings"
	"testing"
	"time"
)

func TestSecurityGuidanceDetectsDangerousCommand(t *testing.T) {
	findings := AnalyzeSecurityGuidance(map[string]any{"command": "sudo rm -rf /"})
	if len(findings) == 0 || findings[0].Rule != "rm-rf-root" {
		t.Fatalf("expected rm-rf-root finding, got %+v", findings)
	}
}

func TestSecurityGuidanceDetectsCredentialsAndUnsafeFiles(t *testing.T) {
	findings := AnalyzeSecurityGuidance(map[string]any{
		"output": "token=abcdefghijklmnopqrstuvwxyz123456",
		"path":   "/etc/passwd",
	})
	var credential, file bool
	for _, f := range findings {
		credential = credential || f.Rule == "generic-api-key"
		file = file || f.Rule == "write-etc"
	}
	if !credential || !file {
		t.Fatalf("missing credential/file findings: %+v", findings)
	}
}

func TestSecurityGuidanceHandlerAppendsWarningsAndRegisters(t *testing.T) {
	mgr := NewManager()
	mgr.Register(&Hook{HookKey: "security-guidance", Source: SourceBundled, Manifest: HookManifest{Metadata: &HookMetaWrap{OpenClaw: &OpenClawHookMeta{Events: []string{"command:new"}}}}})
	RegisterBundledHandlers(mgr, BundledHandlerOpts{})
	if mgr.hooks[0].Handler == nil {
		t.Fatal("security-guidance handler was not registered")
	}
	ev := &Event{Name: "command:new", EventType: "command", Action: "new", Context: map[string]any{"command": "chmod 777 secrets.txt"}, Timestamp: time.Now()}
	if err := mgr.hooks[0].Handler(ev); err != nil {
		t.Fatal(err)
	}
	if len(ev.Messages) == 0 || !strings.Contains(ev.Messages[0], "chmod 777") {
		t.Fatalf("expected chmod warning, got %+v", ev.Messages)
	}
}
