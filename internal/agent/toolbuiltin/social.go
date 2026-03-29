// Package toolbuiltin — social planning agent tools.
//
// Tools:
//   - social_plan_add   — register a recurring social action with schedule
//   - social_plan_list  — list active plans and daily usage
//   - social_plan_remove — remove a plan by ID
//   - social_history    — query recent social action history
//   - social_record     — record a completed social action (for rate limiting)
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"metiq/internal/agent"
	"metiq/internal/social"
)

// ─── Tool Definitions ────────────────────────────────────────────────────────

// SocialPlanAddDef is the ToolDefinition for social_plan_add.
var SocialPlanAddDef = agent.ToolDefinition{
	Name: "social_plan_add",
	Description: "Register a recurring social action plan (post, follow, or engage). " +
		"The plan stores the schedule and instructions. " +
		"Use cron_add separately to wire execution. " +
		"Rate limits are enforced per type (posts/follows/engages per day).",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"id": {
				Type:        "string",
				Description: "Unique plan ID (e.g. \"daily-dev-post\", \"weekly-follow-devs\").",
			},
			"type": {
				Type:        "string",
				Description: "Plan type: \"post\", \"follow\", or \"engage\".",
			},
			"schedule": {
				Type:        "string",
				Description: "Cron expression for cadence (e.g. \"0 */4 * * *\" for every 4 hours, \"@daily\").",
			},
			"instructions": {
				Type:        "string",
				Description: "What to do on each trigger (e.g. \"Write a technical note about Go concurrency\").",
			},
			"tags": {
				Type:        "string",
				Description: "Comma-separated tags for categorisation (e.g. \"nostr,dev,golang\").",
			},
		},
		Required: []string{"id", "type", "schedule", "instructions"},
	},
}

// SocialPlanListDef is the ToolDefinition for social_plan_list.
var SocialPlanListDef = agent.ToolDefinition{
	Name:        "social_plan_list",
	Description: "List all registered social plans and current daily rate limit usage. Shows plan IDs, types, schedules, and how many actions remain today.",
	Parameters:  agent.ToolParameters{Type: "object"},
}

// SocialPlanRemoveDef is the ToolDefinition for social_plan_remove.
var SocialPlanRemoveDef = agent.ToolDefinition{
	Name:        "social_plan_remove",
	Description: "Remove a social plan by its ID.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"id": {
				Type:        "string",
				Description: "The plan ID to remove.",
			},
		},
		Required: []string{"id"},
	},
}

// SocialHistoryDef is the ToolDefinition for social_history.
var SocialHistoryDef = agent.ToolDefinition{
	Name: "social_history",
	Description: "Query recent social action history. " +
		"Returns what was posted, followed, or engaged with, including timestamps and event IDs. " +
		"Use to audit past actions and avoid duplicates.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"type": {
				Type:        "string",
				Description: "Filter by action type: \"post\", \"follow\", \"engage\", or empty for all.",
			},
			"limit": {
				Type:        "integer",
				Description: "Maximum entries to return (default 20, max 100).",
			},
		},
	},
}

// SocialRecordDef is the ToolDefinition for social_record.
var SocialRecordDef = agent.ToolDefinition{
	Name: "social_record",
	Description: "Record a completed social action for rate limiting and history tracking. " +
		"Call after successfully posting, following, or engaging. " +
		"Returns an error if the daily rate limit for this action type has been exceeded.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"plan_id": {
				Type:        "string",
				Description: "The plan ID this action belongs to (optional).",
			},
			"type": {
				Type:        "string",
				Description: "Action type: \"post\", \"follow\", or \"engage\".",
			},
			"action": {
				Type:        "string",
				Description: "Short description of what was done (e.g. \"Posted note about Go channels\").",
			},
			"event_id": {
				Type:        "string",
				Description: "Nostr event ID of the published event (if applicable).",
			},
		},
		Required: []string{"type", "action"},
	},
}

// ─── Tool Implementations ────────────────────────────────────────────────────

// SocialPlanAddTool returns an agent.ToolFunc for social_plan_add.
func SocialPlanAddTool(planner *social.Planner) agent.ToolFunc {
	return func(_ context.Context, args map[string]any) (string, error) {
		id := agent.ArgString(args, "id")
		typ := agent.ArgString(args, "type")
		schedule := agent.ArgString(args, "schedule")
		instructions := agent.ArgString(args, "instructions")

		var tags []string
		if t := agent.ArgString(args, "tags"); t != "" {
			for _, tag := range strings.Split(t, ",") {
				tag = strings.TrimSpace(tag)
				if tag != "" {
					tags = append(tags, tag)
				}
			}
		}

		plan := social.Plan{
			ID:           id,
			Type:         social.PlanType(typ),
			Schedule:     schedule,
			Instructions: instructions,
			Tags:         tags,
			CreatedAt:    time.Now().Unix(),
			Enabled:      true,
		}
		if err := planner.AddPlan(plan); err != nil {
			return "", fmt.Errorf("social_plan_add: %w", err)
		}
		out, _ := json.Marshal(map[string]any{
			"ok":       true,
			"id":       id,
			"type":     typ,
			"schedule": schedule,
		})
		return string(out), nil
	}
}

// SocialPlanListTool returns an agent.ToolFunc for social_plan_list.
func SocialPlanListTool(planner *social.Planner) agent.ToolFunc {
	return func(_ context.Context, _ map[string]any) (string, error) {
		plans := planner.ListPlans()
		usage := planner.DailyUsage()

		type usageRow struct {
			Type      string `json:"type"`
			Used      int    `json:"used_today"`
			Limit     int    `json:"daily_limit"`
			Remaining int    `json:"remaining"`
		}
		usageRows := make([]usageRow, 0, 3)
		for _, typ := range []social.PlanType{social.PlanPost, social.PlanFollow, social.PlanEngage} {
			u := usage[typ]
			usageRows = append(usageRows, usageRow{
				Type:      string(typ),
				Used:      u[0],
				Limit:     u[1],
				Remaining: u[1] - u[0],
			})
		}

		out, _ := json.Marshal(map[string]any{
			"plans":       plans,
			"plan_count":  len(plans),
			"daily_usage": usageRows,
		})
		return string(out), nil
	}
}

// SocialPlanRemoveTool returns an agent.ToolFunc for social_plan_remove.
func SocialPlanRemoveTool(planner *social.Planner) agent.ToolFunc {
	return func(_ context.Context, args map[string]any) (string, error) {
		id := agent.ArgString(args, "id")
		if id == "" {
			return "", fmt.Errorf("social_plan_remove: id is required")
		}
		if !planner.RemovePlan(id) {
			return "", fmt.Errorf("social_plan_remove: plan %q not found", id)
		}
		out, _ := json.Marshal(map[string]any{"removed": true, "id": id})
		return string(out), nil
	}
}

// SocialHistoryTool returns an agent.ToolFunc for social_history.
func SocialHistoryTool(planner *social.Planner) agent.ToolFunc {
	return func(_ context.Context, args map[string]any) (string, error) {
		limit := agent.ArgInt(args, "limit", 20)
		if limit > 100 {
			limit = 100
		}

		typ := agent.ArgString(args, "type")
		var entries []social.HistoryEntry
		if typ != "" {
			entries = planner.HistoryByType(social.PlanType(typ), limit)
		} else {
			entries = planner.RecentHistory(limit)
		}

		out, _ := json.Marshal(map[string]any{
			"entries": entries,
			"count":   len(entries),
		})
		return string(out), nil
	}
}

// SocialRecordTool returns an agent.ToolFunc for social_record.
func SocialRecordTool(planner *social.Planner) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		typ := agent.ArgString(args, "type")
		action := agent.ArgString(args, "action")
		if typ == "" || action == "" {
			return "", fmt.Errorf("social_record: type and action are required")
		}

		entry := social.HistoryEntry{
			PlanID:    agent.ArgString(args, "plan_id"),
			Type:      social.PlanType(typ),
			Action:    action,
			EventID:   agent.ArgString(args, "event_id"),
			Unix:      time.Now().Unix(),
			SessionID: agent.SessionIDFromContext(ctx),
		}

		if err := planner.RecordAction(entry); err != nil {
			return "", fmt.Errorf("social_record: %w", err)
		}
		out, _ := json.Marshal(map[string]any{
			"recorded":        true,
			"type":            typ,
			"remaining_today": planner.RemainingToday(social.PlanType(typ)),
		})
		return string(out), nil
	}
}
