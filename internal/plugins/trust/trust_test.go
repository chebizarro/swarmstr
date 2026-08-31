package trust

import (
	"strings"
	"testing"
)

func TestMutableMetadataNeverGrantsTrust(t *testing.T) {
	for _, source := range []string{"path", "local-dev", "development", "npm", "registry", ""} {
		if got := FromSource(source); got != LevelUntrusted {
			t.Fatalf("FromSource(%q)=%q want untrusted", source, got)
		}
	}
	for _, record := range []map[string]any{
		{"source": "path"},
		{"type": "local"},
		{"source": "npm", "trust": "trusted"},
	} {
		if got := FromInstallRecord(record); got != LevelUntrusted {
			t.Fatalf("FromInstallRecord(%v)=%q want untrusted", record, got)
		}
	}
}

func TestFromIdentityRequiresExactOperatorPolicyMatch(t *testing.T) {
	digest := strings.Repeat("a", 64)
	identity := NewSourceIdentity("SHA256", strings.ToUpper(digest))
	if got := FromIdentity(identity, Policy{}); got != LevelUntrusted {
		t.Fatalf("identity without policy=%q", got)
	}
	policy := Policy{TrustedSourceIdentities: []string{"sha256:" + digest}}
	if got := FromIdentity(identity, policy); got != LevelTrusted {
		t.Fatalf("operator-approved identity=%q", got)
	}
	changed := NewSourceIdentity("sha256", strings.Repeat("b", 64))
	if got := FromIdentity(changed, policy); got != LevelUntrusted {
		t.Fatalf("changed source snapshot=%q", got)
	}
}

func TestFromIdentityRejectsMalformedIdentity(t *testing.T) {
	policy := Policy{TrustedSourceIdentities: []string{"sha256:not-a-digest"}}
	if got := FromIdentity(NewSourceIdentity("sha256", "not-a-digest"), policy); got != LevelUntrusted {
		t.Fatalf("malformed identity=%q", got)
	}
}
