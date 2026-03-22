// Package secure – publish_guard.go provides an outbound content gate that
// prevents sensitive data (secrets, credentials, private keys) from being
// published to Nostr relays.
//
// Every tool that publishes events should call guard.CheckEvent (or
// guard.CheckContent for pre-encryption DM text) before signing and sending.
// The guard is nil-safe: a nil *PublishGuard is a no-op (returns nil error).
//
// Policy modes:
//
//	"block" (default) — reject the publish, return an error to the calling tool.
//	"warn"            — log a warning but allow the publish to proceed.
//	"off"             — disable scanning entirely (for testing only).
package secure

import (
	"fmt"
	"log"
	"strings"

	nostr "fiatjaf.com/nostr"
)

// PublishPolicy controls what happens when sensitive content is detected.
type PublishPolicy string

const (
	// PublishPolicyBlock rejects the publish with an error (default, recommended).
	PublishPolicyBlock PublishPolicy = "block"
	// PublishPolicyWarn logs a warning but allows the publish to proceed.
	PublishPolicyWarn PublishPolicy = "warn"
	// PublishPolicyOff disables the guard entirely (testing only).
	PublishPolicyOff PublishPolicy = "off"
)

// ParsePublishPolicy converts a string to a PublishPolicy.
// Returns PublishPolicyBlock for unrecognised values.
func ParsePublishPolicy(s string) PublishPolicy {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "warn":
		return PublishPolicyWarn
	case "off", "disabled", "none":
		return PublishPolicyOff
	default:
		return PublishPolicyBlock
	}
}

// PublishGuard gates outbound Nostr event publishing by scanning content
// for sensitive data. It is safe to call methods on a nil receiver.
type PublishGuard struct {
	scanner *ContentScanner
	policy  PublishPolicy
}

// NewPublishGuard creates a guard with the given policy.
// When policy is "off", the scanner is not initialised (zero overhead).
func NewPublishGuard(policy PublishPolicy) *PublishGuard {
	if policy == PublishPolicyOff {
		return &PublishGuard{policy: policy}
	}
	return &PublishGuard{
		scanner: NewContentScanner(),
		policy:  policy,
	}
}

// Policy returns the guard's current policy.
func (g *PublishGuard) Policy() PublishPolicy {
	if g == nil {
		return PublishPolicyOff
	}
	return g.policy
}

func (g *PublishGuard) PatternCount() int {
	if g == nil || g.policy == PublishPolicyOff || g.scanner == nil {
		return 0
	}
	return g.scanner.PatternCount()
}

// CheckEvent scans a Nostr event's content and tag values for sensitive data.
// Returns an error when policy is "block" and sensitive content is found.
// Returns nil when the guard is nil, policy is "off", or content is clean.
func (g *PublishGuard) CheckEvent(evt *nostr.Event) error {
	if g == nil || g.policy == PublishPolicyOff || g.scanner == nil || evt == nil {
		return nil
	}

	// Collect all text to scan: event content + all tag values.
	texts := make([]string, 0, 1+len(evt.Tags))
	if evt.Content != "" {
		texts = append(texts, evt.Content)
	}
	for _, tag := range evt.Tags {
		for i := 1; i < len(tag); i++ {
			if tag[i] != "" {
				texts = append(texts, tag[i])
			}
		}
	}

	result := g.scanner.ScanStrings(texts...)
	if result.Clean {
		return nil
	}

	return g.handleFindings(result, fmt.Sprintf("kind=%d", evt.Kind))
}

// CheckContent scans arbitrary text for sensitive data. Use this for
// pre-encryption checks (e.g. DM plaintext before NIP-17/NIP-04 encryption).
// Returns an error when policy is "block" and sensitive content is found.
func (g *PublishGuard) CheckContent(text string) error {
	if g == nil || g.policy == PublishPolicyOff || g.scanner == nil {
		return nil
	}

	result := g.scanner.Scan(text)
	if result.Clean {
		return nil
	}

	return g.handleFindings(result, "content")
}

// handleFindings processes scan findings according to the guard's policy.
func (g *PublishGuard) handleFindings(result ScanResult, context string) error {
	desc := fmt.Sprintf("publish-guard: sensitive content detected [%s]: %s",
		context, result.Summary())

	switch g.policy {
	case PublishPolicyBlock:
		// Include specific finding names in error so the agent sees what triggered it.
		return fmt.Errorf("%s — publish blocked (matched: %s). "+
			"Remove credentials/secrets from content before publishing",
			desc, result.Summary())
	case PublishPolicyWarn:
		log.Printf("WARNING %s — allowing publish (policy=warn)", desc)
		return nil
	default:
		return nil
	}
}
