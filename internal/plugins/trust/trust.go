package trust

import (
	"encoding/hex"
	"strings"
)

// Level classifies whether plugin code is trusted to run with normal host access.
type Level string

const (
	LevelTrusted   Level = "trusted"
	LevelUntrusted Level = "untrusted"
)

func (l Level) String() string {
	if l == LevelTrusted || l == LevelUntrusted {
		return string(l)
	}
	return string(LevelUntrusted)
}

func (l Level) IsTrusted() bool { return l == LevelTrusted }

// SourceIdentity identifies the exact plugin source snapshot that will execute.
// Trust decisions must use this immutable identity, never mutable source labels,
// manifest fields, or install-record metadata.
type SourceIdentity struct {
	Algorithm string
	Digest    string
}

// NewSourceIdentity returns a normalized source identity. Invalid or unsupported
// identities remain invalid and therefore cannot match an operator policy.
func NewSourceIdentity(algorithm, digest string) SourceIdentity {
	return SourceIdentity{
		Algorithm: strings.ToLower(strings.TrimSpace(algorithm)),
		Digest:    strings.ToLower(strings.TrimSpace(digest)),
	}
}

// String returns the canonical operator-policy key for a valid identity.
func (i SourceIdentity) String() string {
	if !i.valid() {
		return ""
	}
	return i.Algorithm + ":" + i.Digest
}

func (i SourceIdentity) valid() bool {
	if i.Algorithm != "sha256" || len(i.Digest) != 64 {
		return false
	}
	_, err := hex.DecodeString(i.Digest)
	return err == nil
}

// Policy is operator-owned trust configuration. Each entry must be the
// canonical identity of an exact source snapshot (for example sha256:<digest>).
type Policy struct {
	TrustedSourceIdentities []string
}

// FromIdentity grants trust only when the exact source snapshot is present in
// operator-owned policy. Invalid identities and malformed policy entries fail
// closed.
func FromIdentity(identity SourceIdentity, policy Policy) Level {
	key := identity.String()
	if key == "" {
		return LevelUntrusted
	}
	for _, allowed := range policy.TrustedSourceIdentities {
		if strings.ToLower(strings.TrimSpace(allowed)) == key {
			return LevelTrusted
		}
	}
	return LevelUntrusted
}

// FromSource is retained for compatibility with persisted install data. Source
// labels are mutable and are never sufficient to grant trust.
func FromSource(string) Level { return LevelUntrusted }

// FromInstallRecord is retained for compatibility with old records. Plugin
// manifests and install metadata are not operator policy and can never grant
// trust.
func FromInstallRecord(map[string]any) Level { return LevelUntrusted }
