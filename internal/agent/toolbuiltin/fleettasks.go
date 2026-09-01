// Package toolbuiltin fleettasks.go — merge-aware fleet task lifecycle tool.
//
// fleet_tasks is the single agent surface for NIP-CAS-0006 peer-to-peer task
// coordination (kind-30900 cascadia.task-state.v2 snapshots), mirroring the
// openclaw-nostr nostr_fleet_tasks contract: one action-based tool whose
// mutations are optimistic-concurrency checked against the current effective
// event ID and always publish complete snapshots.
//
// The live *tasks.FleetTaskBridge is injected via FleetTaskBridgeFunc so the
// tool can be registered before the bridge starts (and degrade gracefully on
// nodes where fleet_tasks is disabled).
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"metiq/internal/agent"
	"metiq/internal/tasks"
)

// FleetTaskBridgeFunc returns the live fleet task bridge, or nil when fleet
// task sync is not active on this node.
type FleetTaskBridgeFunc func() *tasks.FleetTaskBridge

const (
	fleetTaskMaxIDLen       = 128
	fleetTaskMaxTitleLen    = 300
	fleetTaskMaxDescLen     = 4000
	fleetTaskMaxNoteLen     = 4000
	fleetTaskMaxAssigneeLen = 128
	fleetTaskMaxLabels      = 32
	fleetTaskMaxLabelLen    = 100
	fleetTaskMaxEvidence    = 16
	fleetTaskMaxEvidenceLen = 1000
)

var (
	fleetTaskIDPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	fleetTaskEventIDHex   = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	fleetTaskActionFields = map[string]struct{ required, optional []string }{
		"list":       {},
		"inspect":    {required: []string{"task_id"}},
		"create":     {required: []string{"task_id", "title"}, optional: []string{"description", "priority", "labels", "note", "queue", "epic"}},
		"claim":      {required: []string{"task_id", "base_event_id", "assignee"}},
		"checkpoint": {required: []string{"task_id", "base_event_id", "note"}, optional: []string{"evidence"}},
		"block":      {required: []string{"task_id", "base_event_id", "note"}, optional: []string{"evidence"}},
		"handoff":    {required: []string{"task_id", "base_event_id", "note", "assignee"}, optional: []string{"evidence"}},
		"close":      {required: []string{"task_id", "base_event_id", "note", "evidence"}},
	}
)

// FleetTasksDef is the ToolDefinition for fleet_tasks.
var FleetTasksDef = agent.ToolDefinition{
	Name: "fleet_tasks",
	Description: "Coordinate peer-to-peer fleet work via merge-aware NIP-CAS task-state events (kind 30900, cascadia.task-state.v2). " +
		"Actions: list (all merged tasks), inspect (one task with claim resolution), create (new open task), claim (take an unclaimed task), " +
		"checkpoint (progress note), block (mark blocked), handoff (lineage-preserving note; reassignment is forbidden), close (finish with evidence). " +
		"Every mutation except create requires base_event_id — the effective_event_id from a fresh list/inspect. If the task changed concurrently " +
		"the call fails with a conflict; inspect and retry. After claim, inspect again to verify your claim won settlement before doing heavy work. " +
		"This is the ONLY surface for fleet task coordination — the local task_add/task_list queue is not visible to fleet peers.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"action": {
				Type:        "string",
				Description: "Lifecycle action to perform.",
				Enum:        []string{"list", "inspect", "create", "claim", "checkpoint", "block", "handoff", "close"},
			},
			"task_id": {
				Type:        "string",
				Description: "Task identifier (without the task: prefix). Required for every action except list.",
			},
			"base_event_id": {
				Type:        "string",
				Description: "Current effective event id (64-char hex) from list/inspect. Required for claim, checkpoint, block, handoff, and close.",
			},
			"title": {
				Type:        "string",
				Description: "Task title (create only).",
			},
			"description": {
				Type:        "string",
				Description: "Detailed task description (create only).",
			},
			"priority": {
				Type:        "integer",
				Description: "Priority 0 (critical) to 4 (backlog). Default 2. Create only.",
			},
			"labels": {
				Type:        "array",
				Description: "Unique label strings (create only).",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"note": {
				Type:        "string",
				Description: "Progress/acceptance note. Required for checkpoint, block, handoff, and close; optional for create.",
			},
			"evidence": {
				Type:        "array",
				Description: "Stable evidence references (commit hashes, PR URLs, test-run ids). At least one non-blank item is required for close.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"assignee": {
				Type:        "string",
				Description: "Assignee identity. Required for claim and handoff.",
			},
			"queue": {
				Type:        "string",
				Description: "Queue collection id the task belongs to (create only).",
			},
			"epic": {
				Type:        "string",
				Description: "Epic collection id the task belongs to (create only).",
			},
		},
		Required: []string{"action"},
	},
}

// fleetTaskSummary is the compact list-view projection of one merged task.
type fleetTaskSummary struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Status             string   `json:"status"`
	Priority           int      `json:"priority"`
	Assignee           string   `json:"assignee,omitempty"`
	Labels             []string `json:"labels,omitempty"`
	Queue              string   `json:"queue,omitempty"`
	Epic               string   `json:"epic,omitempty"`
	UpdatedAt          string   `json:"updated_at,omitempty"`
	EffectiveEventID   string   `json:"effective_event_id"`
	Resolution         string   `json:"resolution"`
	WinningAssignee    string   `json:"winning_assignee,omitempty"`
	ContendingEvents   int      `json:"contending_events,omitempty"`
	IncompatibleEvents int      `json:"incompatible_events,omitempty"`
}

func fleetTaskSummarize(view tasks.FleetTaskView) fleetTaskSummary {
	summary := fleetTaskSummary{
		ID:                 view.Task.ID,
		Title:              view.Task.Title,
		Status:             view.Task.Status,
		Priority:           view.Task.Priority,
		Assignee:           view.Task.Assignee,
		Labels:             view.Task.Labels,
		Queue:              view.Task.Queue,
		Epic:               view.Task.Epic,
		UpdatedAt:          view.Task.UpdatedAt,
		EffectiveEventID:   view.EffectiveEventID,
		Resolution:         view.Resolution,
		ContendingEvents:   len(view.ContendingEventIDs),
		IncompatibleEvents: len(view.IncompatibleEventIDs),
	}
	if view.Claim != nil {
		summary.WinningAssignee = view.Claim.Assignee
	}
	return summary
}

type fleetTasksRequest struct {
	action      string
	taskID      string
	baseEventID string
	title       string
	description string
	priority    int
	labels      []string
	note        string
	evidence    []string
	assignee    string
	queue       string
	epic        string
}

func parseFleetTasksArgs(args map[string]any) (fleetTasksRequest, error) {
	req := fleetTasksRequest{priority: 2}
	action := strings.ToLower(strings.TrimSpace(agent.ArgString(args, "action")))
	fields, ok := fleetTaskActionFields[action]
	if !ok {
		return req, fmt.Errorf("fleet_tasks: 'action' must be one of list, inspect, create, claim, checkpoint, block, handoff, close")
	}
	req.action = action

	allowed := map[string]struct{}{"action": {}}
	for _, name := range fields.required {
		allowed[name] = struct{}{}
	}
	for _, name := range fields.optional {
		allowed[name] = struct{}{}
	}
	var unexpected []string
	for key := range args {
		if _, ok := allowed[key]; !ok {
			unexpected = append(unexpected, key)
		}
	}
	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		return req, fmt.Errorf("fleet_tasks: action %q does not accept: %s", action, strings.Join(unexpected, ", "))
	}
	for _, name := range fields.required {
		if _, ok := args[name]; !ok {
			return req, fmt.Errorf("fleet_tasks: action %q requires %q", action, name)
		}
	}

	req.taskID = strings.TrimSpace(agent.ArgString(args, "task_id"))
	if _, ok := args["task_id"]; ok && !fleetTaskIDPattern.MatchString(req.taskID) {
		return req, fmt.Errorf("fleet_tasks: 'task_id' must match %s (max %d chars, no task: prefix)", fleetTaskIDPattern.String(), fleetTaskMaxIDLen)
	}
	req.baseEventID = strings.ToLower(strings.TrimSpace(agent.ArgString(args, "base_event_id")))
	if _, ok := args["base_event_id"]; ok && !fleetTaskEventIDHex.MatchString(req.baseEventID) {
		return req, fmt.Errorf("fleet_tasks: 'base_event_id' must be a 64-character hex event id (use effective_event_id from list/inspect)")
	}

	for _, field := range []struct {
		name  string
		value *string
		max   int
	}{
		{"title", &req.title, fleetTaskMaxTitleLen},
		{"description", &req.description, fleetTaskMaxDescLen},
		{"note", &req.note, fleetTaskMaxNoteLen},
		{"assignee", &req.assignee, fleetTaskMaxAssigneeLen},
		{"queue", &req.queue, fleetTaskMaxIDLen},
		{"epic", &req.epic, fleetTaskMaxIDLen},
	} {
		*field.value = strings.TrimSpace(agent.ArgString(args, field.name))
		if _, ok := args[field.name]; !ok {
			continue
		}
		if *field.value == "" {
			return req, fmt.Errorf("fleet_tasks: %q must not be blank", field.name)
		}
		if len(*field.value) > field.max {
			return req, fmt.Errorf("fleet_tasks: %q exceeds %d characters", field.name, field.max)
		}
	}

	req.priority = agent.ArgInt(args, "priority", 2)
	if req.priority < 0 || req.priority > 4 {
		return req, fmt.Errorf("fleet_tasks: 'priority' must be between 0 and 4")
	}

	var err error
	if req.labels, err = fleetTaskStringSlice(args, "labels", fleetTaskMaxLabels, fleetTaskMaxLabelLen, true); err != nil {
		return req, err
	}
	if req.evidence, err = fleetTaskStringSlice(args, "evidence", fleetTaskMaxEvidence, fleetTaskMaxEvidenceLen, false); err != nil {
		return req, err
	}
	if req.action == "close" && len(req.evidence) == 0 {
		return req, fmt.Errorf("fleet_tasks: close requires at least one non-blank evidence item")
	}
	return req, nil
}

func fleetTaskStringSlice(args map[string]any, key string, maxItems, maxLen int, unique bool) ([]string, error) {
	raw, ok := args[key]
	if !ok {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("fleet_tasks: %q must be an array of strings", key)
	}
	if len(items) > maxItems {
		return nil, fmt.Errorf("fleet_tasks: %q accepts at most %d items", key, maxItems)
	}
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		value, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("fleet_tasks: %q must be an array of strings", key)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > maxLen {
			return nil, fmt.Errorf("fleet_tasks: %q items must be at most %d characters", key, maxLen)
		}
		if unique {
			if _, dup := seen[value]; dup {
				return nil, fmt.Errorf("fleet_tasks: %q contains duplicate %q", key, value)
			}
			seen[value] = struct{}{}
		}
		out = append(out, value)
	}
	return out, nil
}

// FleetTasksTool returns the fleet_tasks ToolFunc backed by the live bridge.
func FleetTasksTool(getBridge FleetTaskBridgeFunc) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		req, err := parseFleetTasksArgs(args)
		if err != nil {
			return "", err
		}
		var bridge *tasks.FleetTaskBridge
		if getBridge != nil {
			bridge = getBridge()
		}
		if bridge == nil {
			return "", fmt.Errorf("fleet_tasks: fleet task sync is not active on this node (enable fleet_tasks in config and configure trusted authors)")
		}
		switch req.action {
		case "list":
			views := bridge.FleetTaskViews()
			summaries := make([]fleetTaskSummary, 0, len(views))
			for _, view := range views {
				summaries = append(summaries, fleetTaskSummarize(view))
			}
			raw, _ := json.Marshal(map[string]any{"tasks": summaries, "count": len(summaries)})
			return string(raw), nil
		case "inspect":
			view, ok := bridge.FleetTaskView(req.taskID)
			if !ok {
				return "", fmt.Errorf("fleet_tasks: no effective fleet state for task %q", req.taskID)
			}
			raw, _ := json.Marshal(view)
			return string(raw), nil
		case "create":
			view, err := bridge.CreateFleetTask(ctx, tasks.CreateFleetTaskInput{
				ID: req.taskID, Title: req.title, Description: req.description,
				Priority: req.priority, Labels: req.labels, Note: req.note,
				Queue: req.queue, Epic: req.epic,
			})
			if err != nil {
				return "", err
			}
			raw, _ := json.Marshal(view)
			return string(raw), nil
		case "claim":
			view, err := bridge.ClaimFleetTask(ctx, req.taskID, req.baseEventID, req.assignee)
			if err != nil {
				return "", err
			}
			raw, _ := json.Marshal(map[string]any{
				"task_view": view,
				"note":      "claim published; inspect again after the settlement window to verify winning_claim identifies your claim before doing heavy work",
			})
			return string(raw), nil
		case "checkpoint", "block", "handoff", "close":
			view, err := bridge.AdvanceFleetTask(ctx, tasks.AdvanceFleetTaskInput{
				Op: req.action, TaskID: req.taskID, BaseEventID: req.baseEventID,
				Note: req.note, Evidence: req.evidence, Assignee: req.assignee,
			})
			if err != nil {
				return "", err
			}
			raw, _ := json.Marshal(view)
			return string(raw), nil
		default:
			return "", fmt.Errorf("fleet_tasks: unsupported action %q", req.action)
		}
	}
}
