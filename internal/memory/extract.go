package memory

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"swarmstr/internal/store/state"
)

var splitter = regexp.MustCompile(`[^a-zA-Z0-9]+`)

var stopwords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "and": {}, "or": {}, "to": {}, "of": {}, "in": {}, "on": {},
	"for": {}, "with": {}, "is": {}, "are": {}, "be": {}, "i": {}, "you": {}, "me": {}, "my": {},
}

// minTurnLen is the minimum character length for a turn to be stored in memory.
// Short acks ("ok", "yes", "thanks") don't carry useful information.
const minTurnLen = 20

func ExtractFromTurn(sessionID, role, sourceRef, text string, unix int64) []state.MemoryDoc {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	if unix <= 0 {
		unix = time.Now().Unix()
	}

	// Always store turns that are substantive enough — the retrieval layer
	// handles relevance. Short acks and noise are filtered by minTurnLen.
	const maxMemoryLen = 1024
	candidate := trimmed
	if len(candidate) < minTurnLen {
		return nil
	}
	if len(candidate) > maxMemoryLen {
		candidate = candidate[:maxMemoryLen]
	}

	keywords := extractKeywords(candidate)
	topic := "general"
	if len(keywords) > 0 {
		topic = keywords[0]
	}

	memoryID := fmt.Sprintf("%s:%s:%d", sessionID, sourceRef, unix)
	return []state.MemoryDoc{{
		Version:   1,
		MemoryID:  memoryID,
		Type:      "fact",
		SessionID: sessionID,
		Role:      role,
		SourceRef: sourceRef,
		Text:      candidate,
		Keywords:  keywords,
		Topic:     topic,
		Unix:      unix,
	}}
}

func explicitMemoryPayload(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	prefixes := []string{"remember:", "remember ", "note:", "store this:"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(text[len(prefix):])
		}
	}
	return ""
}

func extractKeywords(text string) []string {
	parts := splitter.Split(strings.ToLower(text), -1)
	seen := map[string]struct{}{}
	out := make([]string, 0, 6)
	for _, p := range parts {
		if len(p) < 3 {
			continue
		}
		if _, stop := stopwords[p]; stop {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
		if len(out) >= 6 {
			break
		}
	}
	return out
}
