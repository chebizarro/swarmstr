package agent

import (
	"testing"
	"time"
)

func TestNormalizeHeartbeatResponse(t *testing.T) {
	tests := []struct {
		name        string
		params      HeartbeatResponseParams
		wantErr     bool
		wantOutcome HeartbeatOutcome
	}{
		{
			name: "valid response",
			params: HeartbeatResponseParams{
				Outcome: "done",
				Notify:  boolPtr(true),
				Summary: "Task completed",
			},
			wantErr:     false,
			wantOutcome: OutcomeDone,
		},
		{
			name: "invalid outcome",
			params: HeartbeatResponseParams{
				Outcome: "invalid",
				Notify:  boolPtr(true),
				Summary: "Test",
			},
			wantErr: true,
		},
		{
			name: "missing notify",
			params: HeartbeatResponseParams{
				Outcome: "done",
				Summary: "Test",
			},
			wantErr: true,
		},
		{
			name: "missing summary",
			params: HeartbeatResponseParams{
				Outcome: "done",
				Notify:  boolPtr(true),
			},
			wantErr: true,
		},
		{
			name: "case insensitive outcome",
			params: HeartbeatResponseParams{
				Outcome: "NO_CHANGE",
				Notify:  boolPtr(false),
				Summary: "Nothing new",
			},
			wantErr:     false,
			wantOutcome: OutcomeNoChange,
		},
		{
			name: "with optional fields",
			params: HeartbeatResponseParams{
				Outcome:          "progress",
				Notify:           boolPtr(true),
				Summary:          "Making progress",
				NotificationText: "Custom notification",
				Reason:           "Because reasons",
				Priority:         "high",
				NextCheck:        "PT30M",
			},
			wantErr:     false,
			wantOutcome: OutcomeProgress,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := NormalizeHeartbeatResponse(tt.params)

			if tt.wantErr {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if result.Outcome != tt.wantOutcome {
				t.Errorf("Outcome = %v, want %v", result.Outcome, tt.wantOutcome)
			}
		})
	}
}

func boolPtr(b bool) *bool {
	return &b
}

func TestParseHeartbeatResponse_Map(t *testing.T) {
	data := map[string]interface{}{
		"outcome": "done",
		"notify":  true,
		"summary": "Task completed successfully",
	}

	result, err := ParseHeartbeatResponse(data)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.Outcome != OutcomeDone {
		t.Errorf("Outcome = %v, want %v", result.Outcome, OutcomeDone)
	}
	if !result.Notify {
		t.Error("Notify should be true")
	}
	if result.Summary != "Task completed successfully" {
		t.Errorf("Summary = %v, want 'Task completed successfully'", result.Summary)
	}
}

func TestParseHeartbeatResponse_JSON(t *testing.T) {
	data := `{"outcome":"blocked","notify":true,"summary":"Waiting for approval","reason":"Need sign-off"}`

	result, err := ParseHeartbeatResponse(data)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.Outcome != OutcomeBlocked {
		t.Errorf("Outcome = %v, want %v", result.Outcome, OutcomeBlocked)
	}
	if result.Reason != "Need sign-off" {
		t.Errorf("Reason = %v, want 'Need sign-off'", result.Reason)
	}
}

func TestParseHeartbeatResponse_SnakeCaseAlias(t *testing.T) {
	data := map[string]interface{}{
		"outcome":           "progress",
		"notify":            true,
		"summary":           "Working on it",
		"notification_text": "Custom text",
		"next_check":        "PT1H",
	}

	result, err := ParseHeartbeatResponse(data)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.NotificationText != "Custom text" {
		t.Errorf("NotificationText = %v, want 'Custom text'", result.NotificationText)
	}
	if result.NextCheck != "PT1H" {
		t.Errorf("NextCheck = %v, want 'PT1H'", result.NextCheck)
	}
}

func TestParseHeartbeatResponse_CamelCaseAlias(t *testing.T) {
	data := map[string]interface{}{
		"outcome":          "progress",
		"notify":           true,
		"summary":          "Working on it",
		"notificationText": "Custom text",
		"nextCheck":        "PT1H",
	}

	result, err := ParseHeartbeatResponse(data)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.NotificationText != "Custom text" {
		t.Errorf("NotificationText = %v, want 'Custom text'", result.NotificationText)
	}
	if result.NextCheck != "PT1H" {
		t.Errorf("NextCheck = %v, want 'PT1H'", result.NextCheck)
	}
}

func TestHeartbeatResponse_GetNotificationText(t *testing.T) {
	tests := []struct {
		name     string
		response HeartbeatResponse
		want     string
	}{
		{
			name: "notify with custom text",
			response: HeartbeatResponse{
				Notify:           true,
				Summary:          "Default summary",
				NotificationText: "Custom notification",
			},
			want: "Custom notification",
		},
		{
			name: "notify without custom text",
			response: HeartbeatResponse{
				Notify:  true,
				Summary: "Default summary",
			},
			want: "Default summary",
		},
		{
			name: "no notify",
			response: HeartbeatResponse{
				Notify:  false,
				Summary: "Default summary",
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.response.GetNotificationText()
			if result != tt.want {
				t.Errorf("GetNotificationText() = %v, want %v", result, tt.want)
			}
		})
	}
}

func TestHeartbeatResponse_Predicates(t *testing.T) {
	tests := []struct {
		name          string
		response      HeartbeatResponse
		wantComplete  bool
		wantBlocked   bool
		wantAttention bool
		wantSilent    bool
	}{
		{
			name:          "done outcome",
			response:      HeartbeatResponse{Outcome: OutcomeDone, Notify: true},
			wantComplete:  true,
			wantBlocked:   false,
			wantAttention: false,
			wantSilent:    false,
		},
		{
			name:          "blocked outcome",
			response:      HeartbeatResponse{Outcome: OutcomeBlocked, Notify: true},
			wantComplete:  false,
			wantBlocked:   true,
			wantAttention: true,
			wantSilent:    false,
		},
		{
			name:          "needs attention outcome",
			response:      HeartbeatResponse{Outcome: OutcomeNeedsAttention, Notify: true},
			wantComplete:  false,
			wantBlocked:   false,
			wantAttention: true,
			wantSilent:    false,
		},
		{
			name:          "silent response",
			response:      HeartbeatResponse{Outcome: OutcomeNoChange, Notify: false},
			wantComplete:  false,
			wantBlocked:   false,
			wantAttention: false,
			wantSilent:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.response.IsComplete(); got != tt.wantComplete {
				t.Errorf("IsComplete() = %v, want %v", got, tt.wantComplete)
			}
			if got := tt.response.IsBlocked(); got != tt.wantBlocked {
				t.Errorf("IsBlocked() = %v, want %v", got, tt.wantBlocked)
			}
			if got := tt.response.NeedsAttention(); got != tt.wantAttention {
				t.Errorf("NeedsAttention() = %v, want %v", got, tt.wantAttention)
			}
			if got := tt.response.IsSilent(); got != tt.wantSilent {
				t.Errorf("IsSilent() = %v, want %v", got, tt.wantSilent)
			}
		})
	}
}

func TestHeartbeatResponse_GetNextCheckDuration(t *testing.T) {
	tests := []struct {
		name      string
		nextCheck string
		want      time.Duration
		wantErr   bool
	}{
		{"Go duration", "30m", 30 * time.Minute, false},
		{"Go duration hours", "2h", 2 * time.Hour, false},
		{"ISO 8601 minutes", "PT30M", 30 * time.Minute, false},
		{"ISO 8601 hours", "PT2H", 2 * time.Hour, false},
		{"ISO 8601 combined", "PT1H30M", 90 * time.Minute, false},
		{"ISO 8601 trailing unitless digits", "PT1H30", 0, true},
		{"empty", "", 0, true},
		{"invalid", "invalid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := HeartbeatResponse{NextCheck: tt.nextCheck}
			got, err := response.GetNextCheckDuration()

			if tt.wantErr {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if got != tt.want {
				t.Errorf("GetNextCheckDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateHeartbeatResponsePayload(t *testing.T) {
	// Test with notification
	response := &HeartbeatResponse{
		Outcome:          OutcomeProgress,
		Notify:           true,
		Summary:          "Making progress",
		NotificationText: "Custom alert",
	}

	payload := CreateHeartbeatResponsePayload(response)

	if payload.Text != "Custom alert" {
		t.Errorf("Text = %v, want 'Custom alert'", payload.Text)
	}
	if payload.ChannelData["openclawHeartbeatResponse"] == nil {
		t.Error("ChannelData should contain response")
	}

	// Test silent response
	response.Notify = false
	payload = CreateHeartbeatResponsePayload(response)

	if payload.Text != HeartbeatToken {
		t.Errorf("Text = %v, want %v", payload.Text, HeartbeatToken)
	}
}

func TestExtractHeartbeatResponse(t *testing.T) {
	// Use a map[string]interface{} to simulate how data comes from JSON
	responseMap := map[string]interface{}{
		"outcome": "done",
		"notify":  true,
		"summary": "Complete",
	}

	channelData := map[string]any{
		"openclawHeartbeatResponse": responseMap,
	}

	extracted := ExtractHeartbeatResponse(channelData)
	if extracted == nil {
		t.Fatal("Expected response, got nil")
	}
	if extracted.Outcome != OutcomeDone {
		t.Errorf("Outcome = %v, want %v", extracted.Outcome, OutcomeDone)
	}

	// Test missing data
	empty := ExtractHeartbeatResponse(map[string]any{})
	if empty != nil {
		t.Error("Expected nil for missing data")
	}
}

func TestHeartbeatResponseToolDefinition(t *testing.T) {
	def := HeartbeatResponseToolDefinition()

	if def.Name != HeartbeatResponseToolName {
		t.Errorf("Name = %v, want %v", def.Name, HeartbeatResponseToolName)
	}
	if def.Description == "" {
		t.Error("Description should not be empty")
	}
	if def.Parameters.Type == "" {
		t.Error("Parameters.Type should not be empty")
	}
	if len(def.Parameters.Required) != 3 {
		t.Errorf("Expected 3 required params, got %d", len(def.Parameters.Required))
	}
}

func TestParseISO8601Duration(t *testing.T) {
	tests := []struct {
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"PT30M", 30 * time.Minute, false},
		{"PT1H", 1 * time.Hour, false},
		{"PT2H30M", 2*time.Hour + 30*time.Minute, false},
		{"PT1H30M15S", 1*time.Hour + 30*time.Minute + 15*time.Second, false},
		{"pt30m", 30 * time.Minute, false}, // lowercase
		{"PT1H30", 0, true},                // trailing unitless digits
		{"P1D", 0, true},                   // days not supported
		{"30M", 0, true},                   // missing PT prefix
		{"PT", 0, true},                    // empty
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseISO8601Duration(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if got != tt.want {
				t.Errorf("parseISO8601Duration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
