package methods

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// PollRequest decode + normalize
// ---------------------------------------------------------------------------

func TestDecodePollParams_ValidObject(t *testing.T) {
	raw := json.RawMessage(`{"to":"npub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq985tur","question":"Lunch?","options":["Pizza","Tacos","Sushi"],"idempotencyKey":"k1"}`)
	req, err := DecodePollParams(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if req.Question != "Lunch?" {
		t.Errorf("question = %q, want %q", req.Question, "Lunch?")
	}
	if len(req.Options) != 3 {
		t.Fatalf("options = %d, want 3", len(req.Options))
	}
	if req.IdempotencyKey != "k1" {
		t.Errorf("idempotencyKey = %q, want %q", req.IdempotencyKey, "k1")
	}
}

func TestDecodePollParams_CamelCaseAliases(t *testing.T) {
	raw := json.RawMessage(`{"to":"npub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq985tur","question":"Best?","options":["A","B"],"maxSelections":2,"durationSeconds":300,"isAnonymous":true,"idempotencyKey":"k2"}`)
	req, err := DecodePollParams(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if req.MaxSelections != 2 {
		t.Errorf("maxSelections = %d, want 2", req.MaxSelections)
	}
	if req.DurationSeconds != 300 {
		t.Errorf("durationSeconds = %d, want 300", req.DurationSeconds)
	}
	if req.IsAnonymous == nil || !*req.IsAnonymous {
		t.Error("isAnonymous should be true")
	}
}

func TestDecodePollParams_EmptyParams(t *testing.T) {
	raw := json.RawMessage(`{}`)
	req, err := DecodePollParams(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	_, err = req.Normalize()
	if err == nil {
		t.Fatal("expected error for empty params")
	}
}

// ---------------------------------------------------------------------------
// PollRequest.Normalize validation
// ---------------------------------------------------------------------------

func TestPollNormalize_RequireTo(t *testing.T) {
	req := PollRequest{Question: "Q?", Options: []string{"A", "B"}}
	_, err := req.Normalize()
	if err == nil || !strings.Contains(err.Error(), "to is required") {
		t.Fatalf("expected 'to is required', got: %v", err)
	}
}

func TestPollNormalize_RequireQuestion(t *testing.T) {
	req := PollRequest{To: "npub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq985tur", Options: []string{"A", "B"}}
	_, err := req.Normalize()
	if err == nil || !strings.Contains(err.Error(), "question is required") {
		t.Fatalf("expected 'question is required', got: %v", err)
	}
}

func TestPollNormalize_RequireMinOptions(t *testing.T) {
	req := PollRequest{To: "npub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq985tur", Question: "Q?", Options: []string{"A"}}
	_, err := req.Normalize()
	if err == nil || !strings.Contains(err.Error(), "at least 2") {
		t.Fatalf("expected 'at least 2 options', got: %v", err)
	}
}

func TestPollNormalize_RejectTooManyOptions(t *testing.T) {
	opts := make([]string, 13)
	for i := range opts {
		opts[i] = "opt"
	}
	req := PollRequest{To: "npub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq985tur", Question: "Q?", Options: opts}
	_, err := req.Normalize()
	if err == nil || !strings.Contains(err.Error(), "at most 12") {
		t.Fatalf("expected 'at most 12 options', got: %v", err)
	}
}

func TestPollNormalize_DurationExclusive(t *testing.T) {
	req := PollRequest{
		To:              "npub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq985tur",
		Question:        "Q?",
		Options:         []string{"A", "B"},
		DurationSeconds: 60,
		DurationHours:   1,
	}
	_, err := req.Normalize()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually exclusive error, got: %v", err)
	}
}

func TestPollNormalize_MaxDurationSeconds(t *testing.T) {
	req := PollRequest{
		To:              "npub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq985tur",
		Question:        "Q?",
		Options:         []string{"A", "B"},
		DurationSeconds: 700000,
	}
	_, err := req.Normalize()
	if err == nil || !strings.Contains(err.Error(), "604800") {
		t.Fatalf("expected duration_seconds max error, got: %v", err)
	}
}

func TestPollNormalize_MaxSelectionsRange(t *testing.T) {
	req := PollRequest{
		To:            "npub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq985tur",
		Question:      "Q?",
		Options:       []string{"A", "B"},
		MaxSelections: 13,
	}
	_, err := req.Normalize()
	if err == nil || !strings.Contains(err.Error(), "max_selections") {
		t.Fatalf("expected max_selections error, got: %v", err)
	}
}

func TestPollNormalize_GeneratesIdempotencyKey(t *testing.T) {
	req := PollRequest{
		To:       "npub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq985tur",
		Question: "Q?",
		Options:  []string{"A", "B"},
	}
	req, err := req.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if !strings.HasPrefix(req.IdempotencyKey, "poll-") {
		t.Errorf("idempotencyKey = %q, want poll- prefix", req.IdempotencyKey)
	}
}

func TestPollNormalize_TrimsWhitespace(t *testing.T) {
	req := PollRequest{
		To:             "  npub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq985tur  ",
		Question:       "  Lunch?  ",
		Options:        []string{"  Pizza  ", "  Tacos  ", ""},
		Channel:        "  NOSTR  ",
		AccountID:      "  acct  ",
		ThreadID:       "  tid  ",
		IdempotencyKey: "  k3  ",
	}
	req, err := req.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if req.To != "npub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq985tur" {
		t.Errorf("to not trimmed: %q", req.To)
	}
	if req.Question != "Lunch?" {
		t.Errorf("question not trimmed: %q", req.Question)
	}
	if len(req.Options) != 2 { // empty option removed
		t.Errorf("options = %v, want 2 items", req.Options)
	}
	if req.Options[0] != "Pizza" {
		t.Errorf("option[0] = %q, want %q", req.Options[0], "Pizza")
	}
	if req.Channel != "nostr" {
		t.Errorf("channel = %q, want %q", req.Channel, "nostr")
	}
	if req.IdempotencyKey != "k3" {
		t.Errorf("idempotencyKey = %q, want %q", req.IdempotencyKey, "k3")
	}
}

func TestPollNormalize_SilentAndAnonymous(t *testing.T) {
	silent := true
	anon := false
	req := PollRequest{
		To:          "npub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq985tur",
		Question:    "Q?",
		Options:     []string{"A", "B"},
		Silent:      &silent,
		IsAnonymous: &anon,
	}
	req, err := req.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if req.Silent == nil || *req.Silent != true {
		t.Error("silent should be true")
	}
	if req.IsAnonymous == nil || *req.IsAnonymous != false {
		t.Error("isAnonymous should be false")
	}
}

// ---------------------------------------------------------------------------
// NormalizePollInput
// ---------------------------------------------------------------------------

func TestNormalizePollInput_Valid(t *testing.T) {
	input := PollInput{
		Question:      "Favorite color?",
		Options:       []string{"Red", "Blue", "Green"},
		MaxSelections: 2,
		DurationHours: 24,
	}
	out, err := NormalizePollInput(input, 0)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if out.Question != "Favorite color?" {
		t.Errorf("question = %q", out.Question)
	}
	if len(out.Options) != 3 {
		t.Errorf("options = %d, want 3", len(out.Options))
	}
	if out.MaxSelections != 2 {
		t.Errorf("maxSelections = %d, want 2", out.MaxSelections)
	}
	if out.DurationHours != 24 {
		t.Errorf("durationHours = %d, want 24", out.DurationHours)
	}
}

func TestNormalizePollInput_DefaultMaxSelections(t *testing.T) {
	input := PollInput{
		Question: "Q?",
		Options:  []string{"A", "B"},
	}
	out, err := NormalizePollInput(input, 0)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if out.MaxSelections != 1 {
		t.Errorf("maxSelections = %d, want 1", out.MaxSelections)
	}
}

func TestNormalizePollInput_EmptyQuestion(t *testing.T) {
	input := PollInput{Question: "  ", Options: []string{"A", "B"}}
	_, err := NormalizePollInput(input, 0)
	if err == nil || !strings.Contains(err.Error(), "question") {
		t.Fatalf("expected question error, got: %v", err)
	}
}

func TestNormalizePollInput_TooFewOptions(t *testing.T) {
	input := PollInput{Question: "Q?", Options: []string{"A"}}
	_, err := NormalizePollInput(input, 0)
	if err == nil || !strings.Contains(err.Error(), "at least 2") {
		t.Fatalf("expected at-least-2 error, got: %v", err)
	}
}

func TestNormalizePollInput_MaxOptionsEnforced(t *testing.T) {
	input := PollInput{Question: "Q?", Options: []string{"A", "B", "C", "D", "E"}}
	_, err := NormalizePollInput(input, 4)
	if err == nil || !strings.Contains(err.Error(), "at most 4") {
		t.Fatalf("expected max-options error, got: %v", err)
	}
}

func TestNormalizePollInput_MaxSelectionsExceedsOptions(t *testing.T) {
	input := PollInput{Question: "Q?", Options: []string{"A", "B"}, MaxSelections: 5}
	_, err := NormalizePollInput(input, 0)
	if err == nil || !strings.Contains(err.Error(), "exceed option count") {
		t.Fatalf("expected exceed error, got: %v", err)
	}
}

func TestNormalizePollInput_DurationExclusive(t *testing.T) {
	input := PollInput{
		Question:        "Q?",
		Options:         []string{"A", "B"},
		DurationSeconds: 60,
		DurationHours:   1,
	}
	_, err := NormalizePollInput(input, 0)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected exclusive error, got: %v", err)
	}
}

func TestNormalizePollInput_TrimsAndFilters(t *testing.T) {
	input := PollInput{
		Question: "  Q?  ",
		Options:  []string{"  A  ", "", "  B  "},
	}
	out, err := NormalizePollInput(input, 0)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if out.Question != "Q?" {
		t.Errorf("question = %q", out.Question)
	}
	if len(out.Options) != 2 {
		t.Errorf("options = %v, want 2", out.Options)
	}
	if out.Options[0] != "A" || out.Options[1] != "B" {
		t.Errorf("options = %v", out.Options)
	}
}

func TestNormalizePollInput_NegativeDurationSeconds(t *testing.T) {
	input := PollInput{Question: "Q?", Options: []string{"A", "B"}, DurationSeconds: -1}
	_, err := NormalizePollInput(input, 0)
	if err == nil || !strings.Contains(err.Error(), "duration_seconds") {
		t.Fatalf("expected duration error, got: %v", err)
	}
}

func TestNormalizePollInput_NegativeDurationHours(t *testing.T) {
	input := PollInput{Question: "Q?", Options: []string{"A", "B"}, DurationHours: -1}
	_, err := NormalizePollInput(input, 0)
	if err == nil || !strings.Contains(err.Error(), "duration_hours") {
		t.Fatalf("expected duration error, got: %v", err)
	}
}

func TestNormalizePollInput_MaxOptionsZeroSkipsCheck(t *testing.T) {
	input := PollInput{Question: "Q?", Options: []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L"}}
	out, err := NormalizePollInput(input, 0)
	if err != nil {
		t.Fatalf("should allow 12 options when maxOptions=0: %v", err)
	}
	if len(out.Options) != 12 {
		t.Errorf("options = %d, want 12", len(out.Options))
	}
}

// ---------------------------------------------------------------------------
// PollResult JSON shape
// ---------------------------------------------------------------------------

func TestPollResult_JSONShape(t *testing.T) {
	r := PollResult{
		MessageID:      "msg-123",
		Channel:        "nostr",
		PollID:         "poll-456",
		ChannelID:      "ch-789",
		ConversationID: "conv-abc",
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	checks := map[string]string{
		"messageId":      "msg-123",
		"channel":        "nostr",
		"pollId":         "poll-456",
		"channelId":      "ch-789",
		"conversationId": "conv-abc",
	}
	for k, want := range checks {
		if m[k] != want {
			t.Errorf("%s = %q, want %q", k, m[k], want)
		}
	}
}

func TestPollResult_OmitsEmpty(t *testing.T) {
	r := PollResult{MessageID: "msg-1"}
	data, _ := json.Marshal(r)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	for _, key := range []string{"channel", "pollId", "channelId", "conversationId"} {
		if _, ok := m[key]; ok {
			t.Errorf("expected %s to be omitted", key)
		}
	}
}
