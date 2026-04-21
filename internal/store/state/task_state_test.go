package state

import (
	"strings"
	"testing"
)

func TestTaskState_IsEmpty(t *testing.T) {
	ts := TaskState{}
	if !ts.IsEmpty() {
		t.Fatal("zero-value TaskState should be empty")
	}
	ts.Brief = "something"
	if ts.IsEmpty() {
		t.Fatal("TaskState with Brief set should not be empty")
	}
}

func TestTaskState_RenderContextBlock_Empty(t *testing.T) {
	ts := TaskState{}
	if ts.RenderContextBlock() != "" {
		t.Fatal("empty TaskState should render empty string")
	}
}

func TestTaskState_RenderContextBlock(t *testing.T) {
	ts := TaskState{
		Brief:         "Build auth module",
		CurrentStage:  "implementing login endpoint",
		Decisions:     []string{"Use JWT tokens", "Store in Redis"},
		Constraints:   []string{"Must support OIDC"},
		OpenQuestions: []string{"Which OIDC provider?"},
		ArtifactRefs:  []string{"internal/auth/login.go"},
		HandoffNote:   "Login endpoint partially done",
		NextAction:    "Add token validation",
	}
	block := ts.RenderContextBlock()
	if block == "" {
		t.Fatal("non-empty TaskState should render a block")
	}
	for _, expected := range []string{
		"[Task State]",
		"Brief: Build auth module",
		"Stage: implementing login endpoint",
		"Next: Add token validation",
		"Use JWT tokens",
		"Must support OIDC",
		"Which OIDC provider?",
		"internal/auth/login.go",
		"Handoff: Login endpoint partially done",
	} {
		if !strings.Contains(block, expected) {
			t.Errorf("expected block to contain %q, got:\n%s", expected, block)
		}
	}
}

func TestTaskState_RenderContextBlock_PartialFields(t *testing.T) {
	ts := TaskState{Brief: "Fix bug", NextAction: "Run tests"}
	block := ts.RenderContextBlock()
	if !strings.Contains(block, "Brief: Fix bug") {
		t.Error("expected Brief in output")
	}
	if !strings.Contains(block, "Next: Run tests") {
		t.Error("expected NextAction in output")
	}
	// Should not contain empty section headers.
	if strings.Contains(block, "Decisions:") {
		t.Error("should not render empty Decisions section")
	}
}

func TestTruncateTaskStateField(t *testing.T) {
	short := "hello"
	if TruncateTaskStateField(short) != "hello" {
		t.Error("should not modify short strings")
	}
	long := strings.Repeat("x", 1000)
	truncated := TruncateTaskStateField(long)
	if len(truncated) != TaskStateMaxFieldChars {
		t.Errorf("expected length %d, got %d", TaskStateMaxFieldChars, len(truncated))
	}
}

func TestAppendCapped_Basic(t *testing.T) {
	var items []string
	items = AppendCapped(items, "first", 3)
	items = AppendCapped(items, "second", 3)
	items = AppendCapped(items, "third", 3)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	// Adding a 4th should drop the oldest.
	items = AppendCapped(items, "fourth", 3)
	if len(items) != 3 {
		t.Fatalf("expected 3 items after cap, got %d", len(items))
	}
	if items[0] != "second" {
		t.Errorf("expected oldest to be dropped, first item is %q", items[0])
	}
}

func TestAppendCapped_Deduplication(t *testing.T) {
	items := []string{"alpha", "beta"}
	items = AppendCapped(items, "alpha", 5)
	if len(items) != 2 {
		t.Fatalf("duplicate should be skipped, got %d items", len(items))
	}
}

func TestAppendCapped_EmptySkipped(t *testing.T) {
	var items []string
	items = AppendCapped(items, "", 5)
	items = AppendCapped(items, "   ", 5)
	if len(items) != 0 {
		t.Fatalf("empty/whitespace items should be skipped, got %d", len(items))
	}
}
