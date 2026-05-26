package memory

import (
	"regexp"
	"sort"
	"strings"
)

// ConceptVocabulary maps normalized concept tags to lexical triggers. It keeps
// semantic tags distinct from plain keywords while remaining deterministic and
// local-only.
type ConceptVocabulary map[string][]string

var DefaultConceptVocabulary = ConceptVocabulary{
	"architecture": {"architecture", "design", "protocol", "interface", "lifecycle"},
	"bug":          {"bug", "error", "failure", "regression", "broken"},
	"preference":   {"prefer", "preference", "like", "dislike", "style"},
	"decision":     {"decision", "decided", "choose", "chosen", "tradeoff"},
	"security":     {"security", "secret", "sandbox", "permission", "auth", "encryption"},
	"nostr":        {"nostr", "relay", "event", "subscription", "nip"},
	"tooling":      {"tool", "cli", "command", "test", "build", "hook"},
	"memory":       {"memory", "recall", "context", "embedding", "compaction", "dreaming"},
}

var conceptTokenRe = regexp.MustCompile(`[a-z0-9_+-]+`)

// DeriveConceptTags returns normalized concept tags (without the concept:
// prefix) for text using the default vocabulary.
func DeriveConceptTags(text string) []string {
	return DeriveConceptTagsWithVocabulary(text, DefaultConceptVocabulary)
}

func DeriveConceptTagsWithVocabulary(text string, vocab ConceptVocabulary) []string {
	if len(vocab) == 0 || strings.TrimSpace(text) == "" {
		return nil
	}
	tokens := map[string]struct{}{}
	for _, token := range conceptTokenRe.FindAllString(strings.ToLower(text), -1) {
		tokens[token] = struct{}{}
	}
	matched := make([]string, 0)
	seen := map[string]struct{}{}
	for concept, triggers := range vocab {
		concept = normalizeConceptTag(concept)
		if concept == "" {
			continue
		}
		for _, trigger := range triggers {
			trigger = strings.ToLower(strings.TrimSpace(trigger))
			if trigger == "" {
				continue
			}
			_, tokenHit := tokens[trigger]
			phraseHit := strings.Contains(strings.ToLower(text), trigger)
			if tokenHit || phraseHit {
				if _, ok := seen[concept]; !ok {
					seen[concept] = struct{}{}
					matched = append(matched, concept)
				}
				break
			}
		}
	}
	sort.Strings(matched)
	return matched
}

func ConceptKeywords(text string) []string {
	concepts := DeriveConceptTags(text)
	out := make([]string, 0, len(concepts))
	for _, concept := range concepts {
		out = append(out, "concept:"+concept)
	}
	return out
}

func normalizeConceptTag(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	tag = strings.ReplaceAll(tag, " ", "_")
	return tag
}
