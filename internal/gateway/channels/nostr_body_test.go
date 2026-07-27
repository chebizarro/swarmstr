package channels

import (
	"strings"
	"testing"
)

func TestBuildNostrGroupBodyForAgent(t *testing.T) {
	const text = "hello world"

	cases := []struct {
		name           string
		pf             NostrPreflightResult
		ambientRespond bool
		wantRaw        bool
		wantPrefix     string
	}{
		{
			name:    "requireMention room passes raw",
			pf:      NostrPreflightResult{RequireMention: true},
			wantRaw: true,
		},
		{
			name:    "effectively mentioned passes raw",
			pf:      NostrPreflightResult{RequireMention: false, EffectiveWasMentioned: true},
			wantRaw: true,
		},
		{
			name:       "ambient scan wraps (default)",
			pf:         NostrPreflightResult{RequireMention: false},
			wantPrefix: nostrBodyScan,
		},
		{
			name:           "ambient respond passes raw",
			pf:             NostrPreflightResult{RequireMention: false},
			ambientRespond: true,
			wantRaw:        true,
		},
		{
			name:           "mentions another participant -> do not respond (even in respond room)",
			pf:             NostrPreflightResult{RequireMention: false, HasAnyMention: true},
			ambientRespond: true,
			wantPrefix:     nostrBodyMentionsOther,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildNostrGroupBodyForAgent(text, tc.pf, tc.ambientRespond)
			if tc.wantRaw {
				if got != text {
					t.Errorf("expected raw body, got %q", got)
				}
				return
			}
			if !strings.HasPrefix(got, tc.wantPrefix) {
				t.Errorf("expected prefix %q, got %q", tc.wantPrefix, got)
			}
			if !strings.HasSuffix(got, text) {
				t.Errorf("wrapped body must still contain the raw text; got %q", got)
			}
		})
	}
}

// The mentions-another-participant guard takes precedence over the ambient
// scan/respond policy (prevents multi-agent crosstalk).
func TestBuildNostrGroupBodyForAgent_MentionOtherPrecedence(t *testing.T) {
	pf := NostrPreflightResult{RequireMention: false, HasAnyMention: true, EffectiveWasMentioned: false}
	got := BuildNostrGroupBodyForAgent("x", pf, false)
	if !strings.HasPrefix(got, nostrBodyMentionsOther) {
		t.Errorf("expected mentions-other guard, got %q", got)
	}
}
