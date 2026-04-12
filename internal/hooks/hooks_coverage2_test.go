package hooks

import (
	"testing"
)

func TestHook_Emoji_WithMeta(t *testing.T) {
	h := &Hook{
		Manifest: HookManifest{
			Metadata: &HookMetaWrap{
				OpenClaw: &OpenClawHookMeta{Emoji: "🔥"},
			},
		},
	}
	if e := h.Emoji(); e != "🔥" {
		t.Errorf("expected 🔥, got %s", e)
	}
}

func TestHook_Emoji_Fallback(t *testing.T) {
	h := &Hook{}
	if e := h.Emoji(); e != "🪝" {
		t.Errorf("expected 🪝, got %s", e)
	}
}

func TestHook_Events(t *testing.T) {
	h := &Hook{
		Manifest: HookManifest{
			Metadata: &HookMetaWrap{
				OpenClaw: &OpenClawHookMeta{Events: []string{"on_start", "on_end"}},
			},
		},
	}
	events := h.Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// Nil metadata
	h2 := &Hook{}
	if events := h2.Events(); events != nil {
		t.Errorf("expected nil, got %v", events)
	}
}

func TestHook_Always(t *testing.T) {
	h := &Hook{
		Manifest: HookManifest{
			Metadata: &HookMetaWrap{
				OpenClaw: &OpenClawHookMeta{Always: true},
			},
		},
	}
	if !h.Always() {
		t.Error("expected Always=true")
	}

	// Default false
	h2 := &Hook{}
	if h2.Always() {
		t.Error("expected Always=false by default")
	}
}

func TestHook_Requires(t *testing.T) {
	reqs := &HookRequires{Bins: []string{"git"}}
	h := &Hook{
		Manifest: HookManifest{
			Metadata: &HookMetaWrap{
				OpenClaw: &OpenClawHookMeta{Requires: reqs},
			},
		},
	}
	got := h.Requires()
	if got == nil || len(got.Bins) != 1 {
		t.Error("expected requires with bins")
	}

	// Nil
	h2 := &Hook{}
	if h2.Requires() != nil {
		t.Error("expected nil requires")
	}
}

func TestHook_InstallSpecs(t *testing.T) {
	specs := []HookInstallSpec{{ID: "test", Kind: "bundled"}}
	h := &Hook{
		Manifest: HookManifest{
			Metadata: &HookMetaWrap{
				OpenClaw: &OpenClawHookMeta{Install: specs},
			},
		},
	}
	got := h.InstallSpecs()
	if len(got) != 1 || got[0].ID != "test" {
		t.Errorf("unexpected install specs: %v", got)
	}

	// Nil
	h2 := &Hook{}
	if h2.InstallSpecs() != nil {
		t.Error("expected nil install specs")
	}
}
