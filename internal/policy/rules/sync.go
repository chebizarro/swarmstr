package rules

import (
	"encoding/json"
	"time"

	"metiq/internal/nostr/adoption"
)

// TeamPolicyKind is a metiq-specific parameterized replaceable event kind for
// relay-synced team policies. The d tag identifies the policy namespace.
const TeamPolicyKind = adoption.KindTeamPolicy

type TeamPolicyBundle struct {
	Version     string    `json:"version"`
	Namespace   string    `json:"namespace"`
	Rules       []Rule    `json:"rules"`
	GeneratedAt time.Time `json:"generated_at"`
}

type DraftEvent = adoption.DraftEvent

func BuildTeamPolicyEvent(bundle TeamPolicyBundle) (DraftEvent, error) {
	if bundle.GeneratedAt.IsZero() {
		bundle.GeneratedAt = time.Now().UTC()
	}
	content, err := json.Marshal(bundle)
	if err != nil {
		return DraftEvent{}, err
	}
	ns := bundle.Namespace
	if ns == "" {
		ns = "default"
	}
	return DraftEvent{
		Kind:      TeamPolicyKind,
		CreatedAt: bundle.GeneratedAt.Unix(),
		Tags:      [][]string{{"d", ns}, {"t", "metiq-policy"}, {"client", "metiq"}},
		Content:   string(content),
	}, nil
}

func ParseTeamPolicyEvent(content string) (TeamPolicyBundle, error) {
	var bundle TeamPolicyBundle
	err := json.Unmarshal([]byte(content), &bundle)
	return bundle, err
}
