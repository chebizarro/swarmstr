package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"metiq/internal/agent"
	"metiq/internal/gateway/sessioncoord"
	"metiq/internal/store/state"
)

const observerAskSystemPrompt = "You answer operator questions about a running AI agent session using only the supplied observation digest. " +
	"Do not infer details that are absent from the observations; plainly say when you cannot know. " +
	"Return only a concise plain-text answer, with no markdown or JSON wrapper."

const (
	observerDigestMaxEntries   = 30
	observerDigestMaxEntryLen  = 400
	observerDigestMaxTotalLen  = 6000
	observerAskContextSuffixID = ":observer"
)

// buildObserverDigest projects the persisted transcript tail into a bounded,
// sanitized observation digest. Raw transcript text is truncated per entry and
// in total before it ever reaches the utility model.
func buildObserverDigest(ctx context.Context, transcriptRepo *state.TranscriptRepository, sessionKey string) (string, error) {
	if transcriptRepo == nil {
		return "", fmt.Errorf("transcript repository unavailable")
	}
	entries, err := transcriptRepo.ListSessionAll(ctx, sessionKey)
	if err != nil {
		return "", err
	}
	if len(entries) > observerDigestMaxEntries {
		entries = entries[len(entries)-observerDigestMaxEntries:]
	}
	type digestEntry struct {
		Role string `json:"role"`
		Text string `json:"text"`
	}
	digest := make([]digestEntry, 0, len(entries))
	total := 0
	for _, entry := range entries {
		text := strings.TrimSpace(entry.Text)
		if text == "" || entry.Role == "deleted" {
			continue
		}
		if runes := []rune(text); len(runes) > observerDigestMaxEntryLen {
			text = string(runes[:observerDigestMaxEntryLen]) + "…"
		}
		total += len(text)
		if total > observerDigestMaxTotalLen {
			break
		}
		digest = append(digest, digestEntry{Role: entry.Role, Text: text})
	}
	encoded, err := json.Marshal(digest)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// newSessionObserverAskProvider wires the sessioncoord observer-ask contract
// to the daemon's per-session runtime resolution and transcript store.
func newSessionObserverAskProvider(transcriptRepo *state.TranscriptRepository, configState *runtimeConfigStore) sessioncoord.ObserverAskProvider {
	return func(ctx context.Context, sessionKey, question string) (string, error) {
		digest, err := buildObserverDigest(ctx, transcriptRepo, sessionKey)
		if err != nil {
			return "", err
		}
		activeAgentID, runtime := resolveInboundChannelRuntime("", sessionKey)
		if runtime == nil {
			return "", fmt.Errorf("no runtime is available for this session")
		}
		prompt := observerAskSystemPrompt + "\n\nObservation digest (JSON):\n" + digest + "\n\nQuestion: " + question
		contextTokens := 0
		if configState != nil {
			contextTokens = maxContextTokensForAgent(configState.Get(), activeAgentID)
		}
		result, err := runtime.ProcessTurn(ctx, agent.Turn{
			SessionID:           sessionKey + observerAskContextSuffixID,
			UserText:            prompt,
			ContextWindowTokens: contextTokens,
		})
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(result.Text), nil
	}
}
