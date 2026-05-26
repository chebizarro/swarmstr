package secrets

import (
	"fmt"
	"regexp"
	"strings"
)

var inlineSecretValue = regexp.MustCompile(`(?i)^[A-Za-z0-9_./+=:-]{16,}$`)

// MigrationChange describes one suggested plaintext-to-reference rewrite.
type MigrationChange struct {
	Path         string `json:"path"`
	Original     string `json:"-"`
	Replacement  string `json:"replacement"`
	SecretName   string `json:"secret_name"`
	RedactedFrom string `json:"redacted_from,omitempty"`
}

// MigrationPlan contains safe, explicit rewrites. It does not mutate input.
type MigrationPlan struct {
	Changes []MigrationChange `json:"changes"`
}

// PlanMigration walks a JSON-like config tree and proposes replacing suspicious
// inline secrets under sensitive keys with env: references. Callers can apply the
// replacements after storing Original in the OS-backed secret store.
func PlanMigration(root map[string]any) MigrationPlan {
	var plan MigrationPlan
	walkMigration("", root, &plan)
	return plan
}

func walkMigration(prefix string, value any, plan *MigrationPlan) {
	switch x := value.(type) {
	case map[string]any:
		for k, v := range x {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			walkMigration(path, v, plan)
		}
	case []any:
		for i, v := range x {
			walkMigration(fmt.Sprintf("%s[%d]", prefix, i), v, plan)
		}
	case string:
		if len(SecretRefs(x)) == 0 && looksSensitivePath(prefix) && inlineSecretValue.MatchString(strings.TrimSpace(x)) {
			name := secretNameFromPath(prefix)
			plan.Changes = append(plan.Changes, MigrationChange{
				Path:         prefix,
				Original:     x,
				Replacement:  "env:" + name,
				SecretName:   name,
				RedactedFrom: NewRedactor(map[string]string{name: x}).RedactString(x),
			})
		}
	}
}

func secretNameFromPath(configPath string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToUpper(configPath) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
		} else if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "METIQ_SECRET"
	}
	return "METIQ_" + out
}
