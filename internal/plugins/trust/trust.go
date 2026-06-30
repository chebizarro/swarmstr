package trust

import "strings"

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

// FromSource classifies an install source. Local path/development installs are
// trusted by default; registry/marketplace/remote installs are untrusted.
func FromSource(source string) Level {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "path", "local", "local-dev", "development", "dev", "file":
		return LevelTrusted
	case "npm", "git", "url", "archive", "registry", "marketplace", "clawhub":
		return LevelUntrusted
	case "":
		return LevelUntrusted
	default:
		return LevelUntrusted
	}
}

// FromInstallRecord classifies a plugin install record or config entry.
func FromInstallRecord(record map[string]any) Level {
	if record == nil {
		return LevelUntrusted
	}
	if explicit, ok := record["trust"].(string); ok {
		switch strings.ToLower(strings.TrimSpace(explicit)) {
		case "trusted", "local", "development", "dev":
			return LevelTrusted
		case "untrusted", "marketplace", "remote":
			return LevelUntrusted
		}
	}
	for _, key := range []string{"source", "type"} {
		if source, ok := record[key].(string); ok && strings.TrimSpace(source) != "" {
			return FromSource(source)
		}
	}
	return LevelUntrusted
}
