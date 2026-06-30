package trust

import "testing"

func TestFromSource(t *testing.T) {
	tests := map[string]Level{
		"path":        LevelTrusted,
		"local-dev":   LevelTrusted,
		"development": LevelTrusted,
		"npm":         LevelUntrusted,
		"git":         LevelUntrusted,
		"url":         LevelUntrusted,
		"registry":    LevelUntrusted,
		"marketplace": LevelUntrusted,
		"":            LevelUntrusted,
	}
	for source, want := range tests {
		if got := FromSource(source); got != want {
			t.Fatalf("FromSource(%q)=%q want %q", source, got, want)
		}
	}
}

func TestFromInstallRecord(t *testing.T) {
	if got := FromInstallRecord(map[string]any{"source": "path"}); got != LevelTrusted {
		t.Fatalf("path record trust=%q", got)
	}
	if got := FromInstallRecord(map[string]any{"type": "marketplace"}); got != LevelUntrusted {
		t.Fatalf("marketplace record trust=%q", got)
	}
	if got := FromInstallRecord(map[string]any{"source": "npm", "trust": "trusted"}); got != LevelTrusted {
		t.Fatalf("explicit trust override=%q", got)
	}
}
