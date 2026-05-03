package agent

import (
	"strings"
	"testing"
)

func TestIsPlanningOnlyTurn(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		state    CommitmentState
		expected bool
	}{
		{
			name:     "promise with no tools",
			text:     "I'll inspect the code, make the change, and run the checks.",
			state:    CommitmentState{ToolCallCount: 0},
			expected: true,
		},
		{
			name:     "promise with tools called",
			text:     "I'll inspect the code, make the change, and run the checks.",
			state:    CommitmentState{ToolCallCount: 1},
			expected: false,
		},
		{
			name:     "let me with no tools",
			text:     "Let me check that for you and get back to you with the results.",
			state:    CommitmentState{ToolCallCount: 0},
			expected: true,
		},
		{
			name:     "completion language exempts",
			text:     "I'll summarize what I found: the issue is in the config file.",
			state:    CommitmentState{ToolCallCount: 0},
			expected: false, // "found" is completion language
		},
		{
			name:     "done statement exempts",
			text:     "Done! I've updated the configuration as requested.",
			state:    CommitmentState{ToolCallCount: 0},
			expected: false,
		},
		{
			name:     "going to with no tools",
			text:     "I'm going to analyze the logs and identify the root cause.",
			state:    CommitmentState{ToolCallCount: 0},
			expected: true,
		},
		{
			name:     "code block exempts",
			text:     "I'll show you the fix:\n```go\nfunc main() {}\n```",
			state:    CommitmentState{ToolCallCount: 0},
			expected: false,
		},
		{
			name:     "empty text",
			text:     "",
			state:    CommitmentState{ToolCallCount: 0},
			expected: false,
		},
		{
			name:     "very long text",
			text:     strings.Repeat("I'll do something. ", 100),
			state:    CommitmentState{ToolCallCount: 0},
			expected: false, // exceeds max length
		},
		{
			name:     "no promise language",
			text:     "The configuration file is located at /etc/config.yaml.",
			state:    CommitmentState{ToolCallCount: 0},
			expected: false,
		},
		{
			name:     "first i'll pattern",
			text:     "First, I'll read the file, then I'll analyze it.",
			state:    CommitmentState{ToolCallCount: 0},
			expected: true,
		},
		{
			name:     "blocker statement exempts",
			text:     "I'll need to check, but the blocker is that I don't have access.",
			state:    CommitmentState{ToolCallCount: 0},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsPlanningOnlyTurn(tt.text, tt.state)
			if got != tt.expected {
				t.Errorf("IsPlanningOnlyTurn(%q) = %v, want %v", tt.text, got, tt.expected)
			}
		})
	}
}

func TestHasUnbackedReminderCommitment(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		state    CommitmentState
		expected bool
	}{
		{
			name:     "reminder promise without cron",
			text:     "I'll remind you tomorrow morning.",
			state:    CommitmentState{SuccessfulCronAdds: 0},
			expected: true,
		},
		{
			name:     "reminder promise with cron",
			text:     "I'll remind you tomorrow morning.",
			state:    CommitmentState{SuccessfulCronAdds: 1},
			expected: false,
		},
		{
			name:     "follow up promise without cron",
			text:     "I'll follow up on this later today.",
			state:    CommitmentState{SuccessfulCronAdds: 0},
			expected: true,
		},
		{
			name:     "check back promise without cron",
			text:     "I'll check back in an hour to see how it's going.",
			state:    CommitmentState{SuccessfulCronAdds: 0},
			expected: true,
		},
		{
			name:     "schedule reminder promise without cron",
			text:     "I'll set a reminder for you.",
			state:    CommitmentState{SuccessfulCronAdds: 0},
			expected: true,
		},
		{
			name:     "no reminder language",
			text:     "I'll analyze the data and provide a report.",
			state:    CommitmentState{SuccessfulCronAdds: 0},
			expected: false,
		},
		{
			name:     "already has warning note",
			text:     "I'll remind you.\n\nNote: I did not schedule a reminder in this turn, so this will not trigger automatically.",
			state:    CommitmentState{SuccessfulCronAdds: 0},
			expected: false,
		},
		{
			name:     "ping promise",
			text:     "I'll ping you when it's done.",
			state:    CommitmentState{SuccessfulCronAdds: 0},
			expected: true,
		},
		{
			name:     "circle back promise",
			text:     "I'll circle back on this tomorrow.",
			state:    CommitmentState{SuccessfulCronAdds: 0},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasUnbackedReminderCommitment(tt.text, tt.state)
			if got != tt.expected {
				t.Errorf("HasUnbackedReminderCommitment(%q) = %v, want %v", tt.text, got, tt.expected)
			}
		})
	}
}

func TestApplyCommitmentGuard(t *testing.T) {
	tests := []struct {
		name            string
		text            string
		state           CommitmentState
		expectModified  bool
		expectContains  string
	}{
		{
			name:            "adds warning for unbacked reminder",
			text:            "I'll remind you tomorrow morning.",
			state:           CommitmentState{SuccessfulCronAdds: 0},
			expectModified:  true,
			expectContains:  UnscheduledReminderNote,
		},
		{
			name:            "no change when cron succeeded",
			text:            "I'll remind you tomorrow morning.",
			state:           CommitmentState{SuccessfulCronAdds: 1},
			expectModified:  false,
			expectContains:  "",
		},
		{
			name:            "no change for normal response",
			text:            "The file has been updated successfully.",
			state:           CommitmentState{SuccessfulCronAdds: 0},
			expectModified:  false,
			expectContains:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, modified := ApplyCommitmentGuard(tt.text, tt.state)
			if modified != tt.expectModified {
				t.Errorf("ApplyCommitmentGuard modified = %v, want %v", modified, tt.expectModified)
			}
			if tt.expectContains != "" && !strings.Contains(got, tt.expectContains) {
				t.Errorf("ApplyCommitmentGuard result should contain %q, got %q", tt.expectContains, got)
			}
		})
	}
}

func TestBuildCommitmentStateFromTraces(t *testing.T) {
	traces := []ToolTrace{
		{Call: ToolCall{Name: "read_file"}, Result: "content"},
		{Call: ToolCall{Name: "cron_add"}, Result: `{"ok":true}`},
		{Call: ToolCall{Name: "bash_exec"}, Error: "command failed"},
		{Call: ToolCall{Name: "cron_add"}, Result: `{"ok":true}`},
	}

	state := BuildCommitmentStateFromTraces(traces)

	if state.ToolCallCount != 4 {
		t.Errorf("ToolCallCount = %d, want 4", state.ToolCallCount)
	}
	if state.SuccessfulCronAdds != 2 {
		t.Errorf("SuccessfulCronAdds = %d, want 2", state.SuccessfulCronAdds)
	}
	if !state.HadMutatingAction {
		t.Error("HadMutatingAction should be true (cron_add succeeded)")
	}
}

func TestCountSuccessfulCronAdds(t *testing.T) {
	tests := []struct {
		name     string
		traces   []ToolTrace
		expected int
	}{
		{
			name:     "no traces",
			traces:   nil,
			expected: 0,
		},
		{
			name: "one successful cron_add",
			traces: []ToolTrace{
				{Call: ToolCall{Name: "cron_add"}, Result: `{"ok":true}`},
			},
			expected: 1,
		},
		{
			name: "failed cron_add not counted",
			traces: []ToolTrace{
				{Call: ToolCall{Name: "cron_add"}, Error: "schedule invalid"},
			},
			expected: 0,
		},
		{
			name: "mixed tools",
			traces: []ToolTrace{
				{Call: ToolCall{Name: "read_file"}, Result: "content"},
				{Call: ToolCall{Name: "cron_add"}, Result: `{"ok":true}`},
				{Call: ToolCall{Name: "cron_add"}, Error: "failed"},
				{Call: ToolCall{Name: "cron_add"}, Result: `{"ok":true}`},
			},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountSuccessfulCronAdds(tt.traces)
			if got != tt.expected {
				t.Errorf("CountSuccessfulCronAdds() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestRecordToolCall(t *testing.T) {
	state := CommitmentState{}

	// Record a read (non-mutating)
	state.RecordToolCall("read_file", false)
	if state.ToolCallCount != 1 {
		t.Errorf("ToolCallCount = %d, want 1", state.ToolCallCount)
	}
	if state.HadMutatingAction {
		t.Error("HadMutatingAction should be false for read_file")
	}

	// Record a cron_add (mutating, successful)
	state.RecordToolCall("cron_add", false)
	if state.SuccessfulCronAdds != 1 {
		t.Errorf("SuccessfulCronAdds = %d, want 1", state.SuccessfulCronAdds)
	}
	if !state.HadMutatingAction {
		t.Error("HadMutatingAction should be true after cron_add")
	}

	// Record a failed cron_add
	state.RecordToolCall("cron_add", true)
	if state.SuccessfulCronAdds != 1 {
		t.Errorf("SuccessfulCronAdds = %d, want 1 (failed call shouldn't count)", state.SuccessfulCronAdds)
	}
	if state.ToolCallCount != 3 {
		t.Errorf("ToolCallCount = %d, want 3", state.ToolCallCount)
	}
}

func TestShouldRetryPlanningOnly(t *testing.T) {
	planningText := "I'll inspect the code and fix the issue."
	completedText := "Done! I've fixed the issue."

	tests := []struct {
		name        string
		text        string
		state       CommitmentState
		retriesUsed int
		maxRetries  int
		expected    bool
	}{
		{
			name:        "should retry planning-only on first attempt",
			text:        planningText,
			state:       CommitmentState{ToolCallCount: 0},
			retriesUsed: 0,
			maxRetries:  1,
			expected:    true,
		},
		{
			name:        "no retry when retries exhausted",
			text:        planningText,
			state:       CommitmentState{ToolCallCount: 0},
			retriesUsed: 1,
			maxRetries:  1,
			expected:    false,
		},
		{
			name:        "no retry for completed response",
			text:        completedText,
			state:       CommitmentState{ToolCallCount: 0},
			retriesUsed: 0,
			maxRetries:  1,
			expected:    false,
		},
		{
			name:        "no retry when tools were called",
			text:        planningText,
			state:       CommitmentState{ToolCallCount: 1},
			retriesUsed: 0,
			maxRetries:  1,
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldRetryPlanningOnly(tt.text, tt.state, tt.retriesUsed, tt.maxRetries)
			if got != tt.expected {
				t.Errorf("ShouldRetryPlanningOnly() = %v, want %v", got, tt.expected)
			}
		})
	}
}
