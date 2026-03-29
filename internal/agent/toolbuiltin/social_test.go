package toolbuiltin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"metiq/internal/social"
)

func TestSocialPlanAddTool(t *testing.T) {
	planner := social.NewPlanner(social.DefaultRateLimitConfig())
	tool := SocialPlanAddTool(planner)

	result, err := tool(context.Background(), map[string]any{
		"id":           "daily-post",
		"type":         "post",
		"schedule":     "0 9 * * *",
		"instructions": "Write a technical note about Go",
		"tags":         "golang,dev",
	})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true {
		t.Error("expected ok=true")
	}
	if out["id"] != "daily-post" {
		t.Errorf("expected id=daily-post, got %v", out["id"])
	}

	// Duplicate should fail.
	_, err = tool(context.Background(), map[string]any{
		"id":           "daily-post",
		"type":         "post",
		"schedule":     "0 9 * * *",
		"instructions": "duplicate",
	})
	if err == nil {
		t.Error("expected error for duplicate plan")
	}
}

func TestSocialPlanListTool(t *testing.T) {
	planner := social.NewPlanner(social.DefaultRateLimitConfig())
	_ = planner.AddPlan(social.Plan{ID: "p1", Type: "post", Schedule: "@daily", Instructions: "test"})
	tool := SocialPlanListTool(planner)

	result, err := tool(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	if out["plan_count"].(float64) != 1 {
		t.Errorf("expected 1 plan, got %v", out["plan_count"])
	}
}

func TestSocialRecordToolRateLimit(t *testing.T) {
	planner := social.NewPlanner(social.RateLimitConfig{PostsPerDay: 1, FollowsPerDay: 5, EngagesPerDay: 5})
	tool := SocialRecordTool(planner)

	// First post should succeed.
	result, err := tool(context.Background(), map[string]any{
		"type":   "post",
		"action": "Posted note about concurrency",
	})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(result), &out)
	if out["remaining_today"].(float64) != 0 {
		t.Errorf("expected 0 remaining, got %v", out["remaining_today"])
	}

	// Second post should hit rate limit.
	_, err = tool(context.Background(), map[string]any{
		"type":   "post",
		"action": "Another post",
	})
	if err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("expected rate limit error, got %v", err)
	}
}

func TestSocialHistoryTool(t *testing.T) {
	planner := social.NewPlanner(social.DefaultRateLimitConfig())
	_ = planner.RecordAction(social.HistoryEntry{Type: "post", Action: "posted note", Unix: 1000})
	_ = planner.RecordAction(social.HistoryEntry{Type: "follow", Action: "followed user", Unix: 1001})
	tool := SocialHistoryTool(planner)

	// All types.
	result, err := tool(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(result), &out)
	if out["count"].(float64) != 2 {
		t.Errorf("expected 2 entries, got %v", out["count"])
	}

	// Filter by type.
	result, err = tool(context.Background(), map[string]any{"type": "post"})
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(result), &out)
	if out["count"].(float64) != 1 {
		t.Errorf("expected 1 post entry, got %v", out["count"])
	}
}

func TestSocialPlanRemoveTool(t *testing.T) {
	planner := social.NewPlanner(social.DefaultRateLimitConfig())
	_ = planner.AddPlan(social.Plan{ID: "x", Type: "engage", Schedule: "@hourly", Instructions: "engage"})
	tool := SocialPlanRemoveTool(planner)

	result, err := tool(context.Background(), map[string]any{"id": "x"})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(result), &out)
	if out["removed"] != true {
		t.Error("expected removed=true")
	}

	// Second remove should fail.
	_, err = tool(context.Background(), map[string]any{"id": "x"})
	if err == nil {
		t.Error("expected error for missing plan")
	}
}
