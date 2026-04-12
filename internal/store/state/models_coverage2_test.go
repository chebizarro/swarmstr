package state

import (
	"testing"
)

func TestParseGoalStatus_AllValues(t *testing.T) {
	valid := []string{"pending", "active", "blocked", "completed", "failed", "cancelled"}
	for _, v := range valid {
		s, ok := ParseGoalStatus(v)
		if !ok {
			t.Errorf("ParseGoalStatus(%q) should be valid", v)
		}
		if string(s) != v {
			t.Errorf("expected %s, got %s", v, s)
		}
	}
	// Case insensitive
	if _, ok := ParseGoalStatus("ACTIVE"); !ok {
		t.Error("should parse uppercase")
	}
	// Invalid
	if _, ok := ParseGoalStatus("bogus"); ok {
		t.Error("should reject bogus")
	}
}

func TestTaskSpec_Validate(t *testing.T) {
	valid := TaskSpec{
		TaskID:       "t-1",
		Title:        "Test",
		Instructions: "Do stuff",
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}

	// Missing task_id
	bad := valid
	bad.TaskID = ""
	if err := bad.Validate(); err == nil {
		t.Error("expected error for empty task_id")
	}

	// Missing title
	bad = valid
	bad.Title = ""
	if err := bad.Validate(); err == nil {
		t.Error("expected error for empty title")
	}

	// Missing instructions
	bad = valid
	bad.Instructions = ""
	if err := bad.Validate(); err == nil {
		t.Error("expected error for empty instructions")
	}

	// Invalid status
	bad = valid
	bad.Status = "bogus"
	if err := bad.Validate(); err == nil {
		t.Error("expected error for invalid status")
	}

	// Invalid priority
	bad = valid
	bad.Priority = "bogus"
	if err := bad.Validate(); err == nil {
		t.Error("expected error for invalid priority")
	}

	// Invalid memory scope
	bad = valid
	bad.MemoryScope = "bogus"
	if err := bad.Validate(); err == nil {
		t.Error("expected error for invalid memory_scope")
	}

	// Empty output name
	bad = valid
	bad.ExpectedOutputs = []TaskOutputSpec{{Name: ""}}
	if err := bad.Validate(); err == nil {
		t.Error("expected error for empty output name")
	}

	// Empty acceptance criteria description
	bad = valid
	bad.AcceptanceCriteria = []TaskAcceptanceCriterion{{Description: ""}}
	if err := bad.Validate(); err == nil {
		t.Error("expected error for empty criterion description")
	}
}

func TestPlanStep_Validate(t *testing.T) {
	valid := PlanStep{
		StepID: "s-1",
		Title:  "Step one",
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}

	// Missing step_id
	bad := valid
	bad.StepID = ""
	if err := bad.Validate(); err == nil {
		t.Error("expected error for empty step_id")
	}

	// Missing title
	bad = valid
	bad.Title = ""
	if err := bad.Validate(); err == nil {
		t.Error("expected error for empty title")
	}

	// Invalid status
	bad = valid
	bad.Status = "bogus"
	if err := bad.Validate(); err == nil {
		t.Error("expected error for invalid status")
	}

	// Empty output name
	bad = valid
	bad.Outputs = []TaskOutputSpec{{Name: ""}}
	if err := bad.Validate(); err == nil {
		t.Error("expected error for empty output name")
	}
}

func TestParseTaskRunStatus_AllValues(t *testing.T) {
	valid := []string{"queued", "running", "blocked", "awaiting_approval", "retrying", "completed", "failed", "cancelled"}
	for _, v := range valid {
		s, ok := ParseTaskRunStatus(v)
		if !ok {
			t.Errorf("ParseTaskRunStatus(%q) should be valid", v)
		}
		if string(s) != v {
			t.Errorf("expected %s, got %s", v, s)
		}
	}
	if _, ok := ParseTaskRunStatus("bogus"); ok {
		t.Error("should reject bogus")
	}
}

func TestParseTaskPriority_AllValues(t *testing.T) {
	valid := []string{"high", "medium", "low"}
	for _, v := range valid {
		p, ok := ParseTaskPriority(v)
		if !ok {
			t.Errorf("ParseTaskPriority(%q) should be valid", v)
		}
		if string(p) != v {
			t.Errorf("expected %s, got %s", v, p)
		}
	}
	// Empty defaults to medium
	p, ok := ParseTaskPriority("")
	if !ok || p != TaskPriorityMedium {
		t.Errorf("empty should default to medium, got %s/%v", p, ok)
	}
	if _, ok := ParseTaskPriority("bogus"); ok {
		t.Error("should reject bogus")
	}
}
