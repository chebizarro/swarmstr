package metrics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// MASTLabel is one bounded multi-agent failure-mode annotation. The labels
// match the openclaw-nostr room-scorecard contract.
type MASTLabel string

const (
	MASTUnnecessaryACK  MASTLabel = "unnecessary_ack"
	MASTEchoRestatement MASTLabel = "echo_restatement"
	MASTDuplicateReply  MASTLabel = "duplicate_response"
	MASTUnbackedCommit  MASTLabel = "unbacked_commitment"
	MASTDroppedCommit   MASTLabel = "dropped_commitment"
	MASTStaleMention    MASTLabel = "stale_mention"
	MASTLoopingPair     MASTLabel = "looping_pair"
	MASTTaskChatShadow  MASTLabel = "task_chat_shadow"
	MASTOther           MASTLabel = "other"
	MASTNone            MASTLabel = "none"
)

var validMASTLabels = map[MASTLabel]struct{}{
	MASTUnnecessaryACK: {}, MASTEchoRestatement: {}, MASTDuplicateReply: {},
	MASTUnbackedCommit: {}, MASTDroppedCommit: {}, MASTStaleMention: {},
	MASTLoopingPair: {}, MASTTaskChatShadow: {}, MASTOther: {}, MASTNone: {},
}

// MASTTranscriptEvent is the bounded transcript shape supplied to a judge.
type MASTTranscriptEvent struct {
	EventID     string `json:"event_id"`
	Author      string `json:"author"`
	CreatedAtMS int64  `json:"created_at_ms"`
	Content     string `json:"content,omitempty"`
}

type MASTJudgeInput struct {
	Room       string                `json:"room"`
	Events     []MASTTranscriptEvent `json:"events"`
	Provider   string                `json:"provider,omitempty"`
	Model      string                `json:"model,omitempty"`
	Experiment string                `json:"experiment,omitempty"`
}

type MASTJudgeOutput struct {
	Labels  []MASTLabel `json:"labels"`
	Summary string      `json:"summary,omitempty"`
}

// MASTJudge is the live-model seam. Implementations must honor ctx cancellation.
type MASTJudge interface {
	Annotate(context.Context, MASTJudgeInput) (MASTJudgeOutput, error)
}

// MASTJudgeResolver selects an installed provider/model implementation.
type MASTJudgeResolver interface {
	ResolveMASTJudge(provider, model string) MASTJudge
}

type MASTJudgePolicy struct {
	Enabled    bool
	Interval   time.Duration
	Timeout    time.Duration
	SampleSize int
	Provider   string
	Model      string
	Experiment string
}

func (p MASTJudgePolicy) normalized() MASTJudgePolicy {
	if p.Interval < time.Minute {
		p.Interval = time.Hour
	}
	if p.Timeout < 100*time.Millisecond || p.Timeout > 30*time.Second {
		p.Timeout = 5 * time.Second
	}
	if p.SampleSize <= 0 || p.SampleSize > 50 {
		p.SampleSize = 50
	}
	p.Provider = strings.TrimSpace(p.Provider)
	p.Model = strings.TrimSpace(p.Model)
	p.Experiment = strings.TrimSpace(p.Experiment)
	if len(p.Experiment) > 100 {
		p.Experiment = p.Experiment[:100]
	}
	return p
}

type MASTJudgeRunStatus string

const (
	MASTRunAnnotated MASTJudgeRunStatus = "annotated"
	MASTRunDisabled  MASTJudgeRunStatus = "disabled"
	MASTRunInterval  MASTJudgeRunStatus = "interval"
	MASTRunUnchanged MASTJudgeRunStatus = "unchanged"
	MASTRunNoJudge   MASTJudgeRunStatus = "no_judge"
	MASTRunError     MASTJudgeRunStatus = "error"
)

type mastJudgeRoomState struct {
	lastAttempt time.Time
	fingerprint string
}

// MASTJudgeScheduler bounds sampling frequency and transcript size, skips
// unchanged transcripts, fails quiet, and joins validated labels to a scorecard.
type MASTJudgeScheduler struct {
	Resolver  MASTJudgeResolver
	Scorecard *RoomScorecard
	Now       func() time.Time

	mu    sync.Mutex
	state map[string]mastJudgeRoomState
}

func (s *MASTJudgeScheduler) Run(ctx context.Context, accountID, room string, policy MASTJudgePolicy, events []MASTTranscriptEvent) MASTJudgeRunStatus {
	policy = policy.normalized()
	if !policy.Enabled {
		return MASTRunDisabled
	}
	nowFn := s.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	scorecard := s.Scorecard
	if scorecard == nil {
		scorecard = DefaultScorecard
	}
	now := nowFn()
	key := strings.TrimSpace(accountID) + "|" + strings.TrimSpace(room)

	s.mu.Lock()
	if s.state == nil {
		s.state = map[string]mastJudgeRoomState{}
	}
	state := s.state[key]
	if !state.lastAttempt.IsZero() && now.Before(state.lastAttempt.Add(policy.Interval)) {
		s.mu.Unlock()
		return MASTRunInterval
	}
	state.lastAttempt = now
	s.state[key] = state
	s.mu.Unlock()

	if len(events) > policy.SampleSize {
		events = events[len(events)-policy.SampleSize:]
	}
	if len(events) == 0 {
		return MASTRunUnchanged
	}
	fingerprint := mastTranscriptFingerprint(events)
	if state.fingerprint == fingerprint {
		return MASTRunUnchanged
	}
	var judge MASTJudge
	if s.Resolver != nil {
		judge = s.Resolver.ResolveMASTJudge(policy.Provider, policy.Model)
	}
	if judge == nil {
		return MASTRunNoJudge
	}

	scorecard.RecordSignal(room, RoomSignalMASTRun)
	judgeCtx, cancel := context.WithTimeout(ctx, policy.Timeout)
	defer cancel()
	output, err := judge.Annotate(judgeCtx, MASTJudgeInput{
		Room: room, Events: append([]MASTTranscriptEvent(nil), events...),
		Provider: policy.Provider, Model: policy.Model, Experiment: policy.Experiment,
	})
	labels, labelErr := normalizeMASTLabels(output.Labels)
	if err != nil || labelErr != nil {
		scorecard.RecordSignal(room, RoomSignalMASTError)
		return MASTRunError
	}
	scorecard.RecordMASTAnnotation(room, labels)
	s.mu.Lock()
	state = s.state[key]
	state.fingerprint = fingerprint
	s.state[key] = state
	s.mu.Unlock()
	return MASTRunAnnotated
}

func normalizeMASTLabels(labels []MASTLabel) ([]MASTLabel, error) {
	seen := map[MASTLabel]struct{}{}
	out := make([]MASTLabel, 0, len(labels))
	for _, label := range labels {
		label = MASTLabel(strings.TrimSpace(string(label)))
		if _, ok := validMASTLabels[label]; !ok {
			continue
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		out = append(out, label)
	}
	if len(out) == 0 || len(out) > 8 {
		return nil, fmt.Errorf("invalid MAST labels")
	}
	if _, none := seen[MASTNone]; none && len(out) > 1 {
		return nil, fmt.Errorf("MAST none label cannot be combined")
	}
	return out, nil
}

func mastTranscriptFingerprint(events []MASTTranscriptEvent) string {
	hash := sha256.New()
	for _, event := range events {
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%d\x00%s\x00", event.EventID, event.Author, event.CreatedAtMS, event.Content)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
