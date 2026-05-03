package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ─── Structured Heartbeat Response ────────────────────────────────────────────
//
// Heartbeat runs can return structured outcomes with fields like outcome,
// notify, priority, and nextCheck instead of freeform text, making heartbeat
// results more actionable.

const (
	// HeartbeatResponseToolName is the canonical tool name
	HeartbeatResponseToolName = "heartbeat_respond"

	// HeartbeatToken is used for silent heartbeat responses
	HeartbeatToken = "[HEARTBEAT]"
)

// HeartbeatOutcome represents the result of a heartbeat check
type HeartbeatOutcome string

const (
	// OutcomeNoChange indicates no changes since last check
	OutcomeNoChange HeartbeatOutcome = "no_change"

	// OutcomeProgress indicates progress has been made
	OutcomeProgress HeartbeatOutcome = "progress"

	// OutcomeDone indicates the task is complete
	OutcomeDone HeartbeatOutcome = "done"

	// OutcomeBlocked indicates the task is blocked
	OutcomeBlocked HeartbeatOutcome = "blocked"

	// OutcomeNeedsAttention indicates user attention is needed
	OutcomeNeedsAttention HeartbeatOutcome = "needs_attention"
)

// ValidOutcomes is the set of valid heartbeat outcomes
var ValidOutcomes = map[HeartbeatOutcome]bool{
	OutcomeNoChange:       true,
	OutcomeProgress:       true,
	OutcomeDone:           true,
	OutcomeBlocked:        true,
	OutcomeNeedsAttention: true,
}

// HeartbeatPriority represents the notification priority
type HeartbeatPriority string

const (
	PriorityLow    HeartbeatPriority = "low"
	PriorityNormal HeartbeatPriority = "normal"
	PriorityHigh   HeartbeatPriority = "high"
)

// ValidPriorities is the set of valid heartbeat priorities
var ValidPriorities = map[HeartbeatPriority]bool{
	PriorityLow:    true,
	PriorityNormal: true,
	PriorityHigh:   true,
}

// HeartbeatResponse is a structured response from a heartbeat run
type HeartbeatResponse struct {
	// Outcome indicates what happened during the heartbeat check
	Outcome HeartbeatOutcome `json:"outcome"`

	// Notify indicates whether the user should be notified
	Notify bool `json:"notify"`

	// Summary is a brief description of the heartbeat result
	Summary string `json:"summary"`

	// NotificationText is the text to send if notifying (defaults to Summary)
	NotificationText string `json:"notification_text,omitempty"`

	// Reason provides additional context for the outcome
	Reason string `json:"reason,omitempty"`

	// Priority indicates the urgency of any notification
	Priority HeartbeatPriority `json:"priority,omitempty"`

	// NextCheck suggests when to check again (ISO 8601 duration or timestamp)
	NextCheck string `json:"next_check,omitempty"`
}

// HeartbeatResponseParams are the raw parameters from the tool call
type HeartbeatResponseParams struct {
	Outcome          string `json:"outcome"`
	Notify           *bool  `json:"notify"`
	Summary          string `json:"summary"`
	NotificationText string `json:"notification_text,omitempty"`
	Reason           string `json:"reason,omitempty"`
	Priority         string `json:"priority,omitempty"`
	NextCheck        string `json:"next_check,omitempty"`
}

// NormalizeHeartbeatResponse validates and normalizes heartbeat response params
func NormalizeHeartbeatResponse(params HeartbeatResponseParams) (*HeartbeatResponse, error) {
	// Validate required fields
	outcome := HeartbeatOutcome(strings.TrimSpace(strings.ToLower(params.Outcome)))
	if !ValidOutcomes[outcome] {
		return nil, fmt.Errorf("invalid outcome %q: must be one of no_change, progress, done, blocked, needs_attention", params.Outcome)
	}

	if params.Notify == nil {
		return nil, fmt.Errorf("notify is required")
	}

	summary := strings.TrimSpace(params.Summary)
	if summary == "" {
		return nil, fmt.Errorf("summary is required")
	}

	response := &HeartbeatResponse{
		Outcome: outcome,
		Notify:  *params.Notify,
		Summary: summary,
	}

	// Optional fields
	if text := strings.TrimSpace(params.NotificationText); text != "" {
		response.NotificationText = text
	}

	if reason := strings.TrimSpace(params.Reason); reason != "" {
		response.Reason = reason
	}

	if priority := HeartbeatPriority(strings.TrimSpace(strings.ToLower(params.Priority))); priority != "" {
		if ValidPriorities[priority] {
			response.Priority = priority
		}
	}

	if nextCheck := strings.TrimSpace(params.NextCheck); nextCheck != "" {
		response.NextCheck = nextCheck
	}

	return response, nil
}

// ParseHeartbeatResponse parses a heartbeat response from raw JSON/map
func ParseHeartbeatResponse(data interface{}) (*HeartbeatResponse, error) {
	var params HeartbeatResponseParams

	switch v := data.(type) {
	case map[string]interface{}:
		// Extract fields from map
		if outcome, ok := v["outcome"].(string); ok {
			params.Outcome = outcome
		}
		if notify, ok := v["notify"].(bool); ok {
			params.Notify = &notify
		}
		if summary, ok := v["summary"].(string); ok {
			params.Summary = summary
		}
		if text, ok := v["notification_text"].(string); ok {
			params.NotificationText = text
		} else if text, ok := v["notificationText"].(string); ok {
			params.NotificationText = text
		}
		if reason, ok := v["reason"].(string); ok {
			params.Reason = reason
		}
		if priority, ok := v["priority"].(string); ok {
			params.Priority = priority
		}
		if nextCheck, ok := v["next_check"].(string); ok {
			params.NextCheck = nextCheck
		} else if nextCheck, ok := v["nextCheck"].(string); ok {
			params.NextCheck = nextCheck
		}

	case string:
		if err := json.Unmarshal([]byte(v), &params); err != nil {
			return nil, fmt.Errorf("failed to parse JSON: %w", err)
		}

	case []byte:
		if err := json.Unmarshal(v, &params); err != nil {
			return nil, fmt.Errorf("failed to parse JSON: %w", err)
		}

	default:
		return nil, fmt.Errorf("unsupported data type: %T", data)
	}

	return NormalizeHeartbeatResponse(params)
}

// GetNotificationText returns the text to use for notifications
func (r *HeartbeatResponse) GetNotificationText() string {
	if !r.Notify {
		return ""
	}
	if r.NotificationText != "" {
		return strings.TrimSpace(r.NotificationText)
	}
	return strings.TrimSpace(r.Summary)
}

// ShouldNotify returns whether a notification should be sent
func (r *HeartbeatResponse) ShouldNotify() bool {
	return r.Notify
}

// IsSilent returns whether this is a silent (no-notification) response
func (r *HeartbeatResponse) IsSilent() bool {
	return !r.Notify
}

// IsComplete returns whether the outcome indicates task completion
func (r *HeartbeatResponse) IsComplete() bool {
	return r.Outcome == OutcomeDone
}

// IsBlocked returns whether the outcome indicates a blocked state
func (r *HeartbeatResponse) IsBlocked() bool {
	return r.Outcome == OutcomeBlocked
}

// NeedsAttention returns whether the outcome requires user attention
func (r *HeartbeatResponse) NeedsAttention() bool {
	return r.Outcome == OutcomeNeedsAttention || r.Outcome == OutcomeBlocked
}

// GetNextCheckDuration parses NextCheck as a duration
func (r *HeartbeatResponse) GetNextCheckDuration() (time.Duration, error) {
	if r.NextCheck == "" {
		return 0, fmt.Errorf("no next_check specified")
	}

	// Try parsing as Go duration (e.g., "30m", "1h")
	if d, err := time.ParseDuration(r.NextCheck); err == nil {
		return d, nil
	}

	// Try parsing as ISO 8601 duration (e.g., "PT30M", "PT1H")
	if strings.HasPrefix(strings.ToUpper(r.NextCheck), "PT") {
		return parseISO8601Duration(r.NextCheck)
	}

	return 0, fmt.Errorf("unsupported duration format: %s", r.NextCheck)
}

// GetNextCheckTime parses NextCheck as an absolute time
func (r *HeartbeatResponse) GetNextCheckTime() (time.Time, error) {
	if r.NextCheck == "" {
		return time.Time{}, fmt.Errorf("no next_check specified")
	}

	// Try parsing as RFC3339
	if t, err := time.Parse(time.RFC3339, r.NextCheck); err == nil {
		return t, nil
	}

	// Try parsing as date-time
	if t, err := time.Parse("2006-01-02T15:04:05", r.NextCheck); err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("unsupported time format: %s", r.NextCheck)
}

// parseISO8601Duration parses a simple ISO 8601 duration (PT format)
func parseISO8601Duration(s string) (time.Duration, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if !strings.HasPrefix(s, "PT") {
		return 0, fmt.Errorf("ISO 8601 duration must start with PT")
	}
	s = s[2:] // Remove "PT"

	if s == "" {
		return 0, fmt.Errorf("empty duration after PT prefix")
	}

	var d time.Duration
	var num string

	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			num += string(c)
		} else {
			if num == "" {
				return 0, fmt.Errorf("missing number before unit %c", c)
			}
			var n int
			if _, err := fmt.Sscanf(num, "%d", &n); err != nil {
				return 0, fmt.Errorf("invalid duration number %q", num)
			}
			num = ""

			switch c {
			case 'H':
				d += time.Duration(n) * time.Hour
			case 'M':
				d += time.Duration(n) * time.Minute
			case 'S':
				d += time.Duration(n) * time.Second
			default:
				return 0, fmt.Errorf("unknown duration unit: %c", c)
			}
		}
	}
	if num != "" {
		return 0, fmt.Errorf("missing duration unit after number %q", num)
	}

	return d, nil
}

// ToJSON serializes the response to JSON
func (r *HeartbeatResponse) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}

// ─── Heartbeat Response Payload ───────────────────────────────────────────────

// HeartbeatResponsePayload wraps a response for channel delivery
type HeartbeatResponsePayload struct {
	Text        string             `json:"text"`
	ChannelData map[string]any     `json:"channel_data,omitempty"`
	Response    *HeartbeatResponse `json:"-"`
}

// CreateHeartbeatResponsePayload creates a payload from a response
func CreateHeartbeatResponsePayload(response *HeartbeatResponse) HeartbeatResponsePayload {
	text := HeartbeatToken
	if response.Notify {
		text = response.GetNotificationText()
	}

	return HeartbeatResponsePayload{
		Text: text,
		ChannelData: map[string]any{
			"openclawHeartbeatResponse": response,
		},
		Response: response,
	}
}

// ExtractHeartbeatResponse extracts a heartbeat response from payload channel data
func ExtractHeartbeatResponse(channelData map[string]any) *HeartbeatResponse {
	data := channelData["openclawHeartbeatResponse"]
	if data == nil {
		return nil
	}

	response, err := ParseHeartbeatResponse(data)
	if err != nil {
		return nil
	}
	return response
}

// ─── Tool Definition ──────────────────────────────────────────────────────────

// HeartbeatResponseToolDefinition returns the tool definition for heartbeat_respond
func HeartbeatResponseToolDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        HeartbeatResponseToolName,
		Description: "Record the result of a heartbeat run. Use notify=false when nothing should be sent visibly. Use notify=true with notification_text when the user should receive a concise heartbeat alert.",
		Parameters: ToolParameters{
			Type: "object",
			Properties: map[string]ToolParamProp{
				"outcome": {
					Type:        "string",
					Enum:        []string{"no_change", "progress", "done", "blocked", "needs_attention"},
					Description: "The result of the heartbeat check",
				},
				"notify": {
					Type:        "boolean",
					Description: "Whether to notify the user",
				},
				"summary": {
					Type:        "string",
					Description: "Brief description of the heartbeat result",
				},
				"notification_text": {
					Type:        "string",
					Description: "Text to send if notifying (defaults to summary)",
				},
				"reason": {
					Type:        "string",
					Description: "Additional context for the outcome",
				},
				"priority": {
					Type:        "string",
					Enum:        []string{"low", "normal", "high"},
					Description: "Urgency of any notification",
				},
				"next_check": {
					Type:        "string",
					Description: "When to check again (ISO 8601 duration like PT30M or timestamp)",
				},
			},
			Required: []string{"outcome", "notify", "summary"},
		},
	}
}
