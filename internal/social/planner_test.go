package social

import (
	"testing"
	"time"
)

func TestAddPlanValidation(t *testing.T) {
	p := NewPlanner(DefaultRateLimitConfig())
	tests := []struct {
		name string
		plan Plan
		want string
	}{
		{"empty ID", Plan{Type: PlanPost, Schedule: "@daily", Instructions: "post"}, "plan ID is required"},
		{"empty type", Plan{ID: "p1", Schedule: "@daily", Instructions: "post"}, "plan type is required"},
		{"empty schedule", Plan{ID: "p1", Type: PlanPost, Instructions: "post"}, "plan schedule is required"},
		{"empty instructions", Plan{ID: "p1", Type: PlanPost, Schedule: "@daily"}, "plan instructions are required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.AddPlan(tt.plan)
			if err == nil || !contains(err.Error(), tt.want) {
				t.Errorf("AddPlan() = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestAddPlanDuplicate(t *testing.T) {
	p := NewPlanner(DefaultRateLimitConfig())
	plan := Plan{ID: "p1", Type: PlanPost, Schedule: "@daily", Instructions: "post something"}
	if err := p.AddPlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := p.AddPlan(plan); err == nil {
		t.Error("expected duplicate error")
	}
}

func TestListPlansOrder(t *testing.T) {
	p := NewPlanner(DefaultRateLimitConfig())
	_ = p.AddPlan(Plan{ID: "b", Type: PlanPost, Schedule: "@daily", Instructions: "second", CreatedAt: 200})
	_ = p.AddPlan(Plan{ID: "a", Type: PlanFollow, Schedule: "@weekly", Instructions: "first", CreatedAt: 100})
	plans := p.ListPlans()
	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(plans))
	}
	if plans[0].ID != "a" || plans[1].ID != "b" {
		t.Errorf("expected [a,b], got [%s,%s]", plans[0].ID, plans[1].ID)
	}
}

func TestRemovePlan(t *testing.T) {
	p := NewPlanner(DefaultRateLimitConfig())
	_ = p.AddPlan(Plan{ID: "x", Type: PlanEngage, Schedule: "@hourly", Instructions: "engage"})
	if !p.RemovePlan("x") {
		t.Error("expected true for existing plan")
	}
	if p.RemovePlan("x") {
		t.Error("expected false for already-removed plan")
	}
}

func TestRecordActionRateLimit(t *testing.T) {
	p := NewPlanner(RateLimitConfig{PostsPerDay: 2, FollowsPerDay: 5, EngagesPerDay: 5})
	now := time.Now().Unix()

	// Two posts should succeed.
	for i := 0; i < 2; i++ {
		err := p.RecordAction(HistoryEntry{PlanID: "p1", Type: PlanPost, Action: "posted", Unix: now})
		if err != nil {
			t.Fatalf("post %d: unexpected error: %v", i+1, err)
		}
	}
	// Third should fail.
	err := p.RecordAction(HistoryEntry{PlanID: "p1", Type: PlanPost, Action: "posted", Unix: now})
	if err == nil {
		t.Error("expected rate limit error for third post")
	}

	// Follows should still be allowed.
	err = p.RecordAction(HistoryEntry{PlanID: "f1", Type: PlanFollow, Action: "followed", Unix: now})
	if err != nil {
		t.Errorf("follow should be allowed: %v", err)
	}
}

func TestRecentHistory(t *testing.T) {
	p := NewPlanner(DefaultRateLimitConfig())
	for i := 0; i < 5; i++ {
		_ = p.RecordAction(HistoryEntry{
			PlanID: "p1", Type: PlanPost, Action: "posted",
			Unix: int64(1000 + i),
		})
	}
	h := p.RecentHistory(3)
	if len(h) != 3 {
		t.Fatalf("expected 3, got %d", len(h))
	}
	// Newest first.
	if h[0].Unix != 1004 || h[2].Unix != 1002 {
		t.Errorf("wrong order: %d, %d", h[0].Unix, h[2].Unix)
	}
}

func TestDailyUsage(t *testing.T) {
	p := NewPlanner(RateLimitConfig{PostsPerDay: 10, FollowsPerDay: 20, EngagesPerDay: 30})
	now := time.Now().Unix()
	_ = p.RecordAction(HistoryEntry{Type: PlanPost, Action: "post", Unix: now})
	_ = p.RecordAction(HistoryEntry{Type: PlanPost, Action: "post", Unix: now})
	_ = p.RecordAction(HistoryEntry{Type: PlanFollow, Action: "follow", Unix: now})

	usage := p.DailyUsage()
	if usage[PlanPost][0] != 2 || usage[PlanPost][1] != 10 {
		t.Errorf("post usage: got %v", usage[PlanPost])
	}
	if usage[PlanFollow][0] != 1 || usage[PlanFollow][1] != 20 {
		t.Errorf("follow usage: got %v", usage[PlanFollow])
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	p := NewPlanner(DefaultRateLimitConfig())
	_ = p.AddPlan(Plan{ID: "a", Type: PlanPost, Schedule: "@daily", Instructions: "post daily"})
	_ = p.RecordAction(HistoryEntry{PlanID: "a", Type: PlanPost, Action: "posted", Unix: 1000})

	plansJSON, err := p.MarshalPlans()
	if err != nil {
		t.Fatal(err)
	}
	histJSON, err := p.MarshalHistory()
	if err != nil {
		t.Fatal(err)
	}

	p2 := NewPlanner(DefaultRateLimitConfig())
	if err := p2.LoadPlans(plansJSON); err != nil {
		t.Fatal(err)
	}
	if err := p2.LoadHistory(histJSON); err != nil {
		t.Fatal(err)
	}
	if len(p2.ListPlans()) != 1 {
		t.Error("plans not loaded")
	}
	if len(p2.RecentHistory(10)) != 1 {
		t.Error("history not loaded")
	}
}

func TestCompactHistory(t *testing.T) {
	p := NewPlanner(DefaultRateLimitConfig())
	old := time.Now().Add(-48 * time.Hour).Unix()
	recent := time.Now().Unix()
	_ = p.RecordAction(HistoryEntry{Type: PlanPost, Action: "old", Unix: old})
	_ = p.RecordAction(HistoryEntry{Type: PlanPost, Action: "recent", Unix: recent})

	removed := p.CompactHistory(24 * time.Hour)
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}
	if len(p.RecentHistory(10)) != 1 {
		t.Error("expected 1 remaining entry")
	}
}

func TestRemainingToday(t *testing.T) {
	p := NewPlanner(RateLimitConfig{PostsPerDay: 3, FollowsPerDay: 5, EngagesPerDay: 5})
	now := time.Now().Unix()
	_ = p.RecordAction(HistoryEntry{Type: PlanPost, Action: "post", Unix: now})

	if r := p.RemainingToday(PlanPost); r != 2 {
		t.Errorf("expected 2 remaining, got %d", r)
	}
	if r := p.RemainingToday(PlanFollow); r != 5 {
		t.Errorf("expected 5 remaining, got %d", r)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
