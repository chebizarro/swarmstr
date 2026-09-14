package main

import (
	"context"
	"encoding/json"
	"strings"

	"metiq/internal/nostr/refresolve"
	nostrmeta "metiq/internal/nostr/metadata"
)

// renderReferencedBlock resolves and renders referenced Nostr events into a
// context block. Returns "" when nothing resolves or the feature is disabled.
func renderReferencedBlock(ctx context.Context, resolver refresolve.Resolver, thread *nostrmeta.ThreadRefs, selfEventID string, cfg referenceContextConfig) string {
	if !cfg.Enabled || cfg.MaxRefs == 0 || resolver == nil || thread == nil {
		return ""
	}

	refs := refresolve.SelectRefs(thread, selfEventID)
	if len(refs) == 0 {
		return ""
	}

	if cfg.MaxRefs > 0 && len(refs) > cfg.MaxRefs {
		refs = refs[:cfg.MaxRefs]
	}

	results := resolver.Resolve(ctx, refs)
	if len(results) == 0 {
		return ""
	}

	for i, msg := range results {
		if (msg.Kind == 6 || msg.Kind == 16) && msg.Body != "" {
			if inner := parseRepostBody(msg.Body); inner != "" {
				msg.Body = "repost of: " + inner
				results[i] = msg
			}
		}
	}

	block := refresolve.RenderReferencedBlock(results)
	if cfg.MaxBlockChars > 0 && len([]rune(block)) > cfg.MaxBlockChars {
		block = string([]rune(block)[:cfg.MaxBlockChars]) + "\n- …(truncated)\n"
	}
	return block
}

// parseRepostBody attempts to parse a kind 6/16 repost's Content (JSON
// encoding of the inner event) and extract the inner event's text content.
// The body has already been normalizeText'd (control chars stripped,
// backtick fences neutralized, len-capped) by the resolver, so a full
// json.Unmarshal may fail on truncation; falls back to a bounded scan for
// the "content" field.
func parseRepostBody(content string) string {
	if content == "" {
		return ""
	}
	var inner struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(content), &inner); err == nil {
		if s, ok := nostrmeta.NormalizeText(inner.Content, refresolve.MaxBodyChars); ok {
			return s
		}
	}
	if s, ok := boundedRepostContent(content); ok {
		return s
	}
	return ""
}

// boundedRepostContent scans a (possibly truncated) repost JSON payload for
// the "content" field and extracts the (JSON-unescaped) value. Stops at the
// first unescaped closing quote; len-safe.
func boundedRepostContent(s string) (string, bool) {
	idx := strings.Index(s, `"content":"`)
	if idx < 0 {
		return "", false
	}
	start := idx + len(`"content":"`)
	if start >= len(s) {
		return "", false
	}
	var b strings.Builder
	escaped := false
	for _, r := range s[start:] {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			break
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "", false
	}
	out, ok := nostrmeta.NormalizeText(b.String(), refresolve.MaxBodyChars)
	return out, ok
}
