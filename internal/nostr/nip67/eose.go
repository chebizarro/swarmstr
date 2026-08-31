// Package nip67 parses EOSE completeness hints without changing NIP-01
// realtime subscription semantics.
package nip67

import (
	"encoding/json"
	"fmt"
)

const (
	HintFinish = "finish"
	HintMore   = "more"
)

type Completeness int

const (
	Unknown Completeness = iota
	Complete
	More
)

type EOSE struct {
	SubscriptionID string
	Hints          []string
}

// ParseEOSE consumes both legacy two-element EOSE and NIP-67 hinted EOSE.
// Unknown hints are preserved and ignored by CompletenessHint.
func ParseEOSE(payload []byte) (EOSE, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil {
		return EOSE{}, err
	}
	if len(raw) < 2 || len(raw) > 3 {
		return EOSE{}, fmt.Errorf("NIP-67: invalid EOSE element count %d", len(raw))
	}
	var label, subID string
	if err := json.Unmarshal(raw[0], &label); err != nil || label != "EOSE" {
		return EOSE{}, fmt.Errorf("NIP-67: invalid EOSE label")
	}
	if err := json.Unmarshal(raw[1], &subID); err != nil || subID == "" {
		return EOSE{}, fmt.Errorf("NIP-67: subscription id required")
	}
	out := EOSE{SubscriptionID: subID}
	if len(raw) == 3 {
		if err := json.Unmarshal(raw[2], &out.Hints); err != nil {
			return EOSE{}, fmt.Errorf("NIP-67: invalid hints: %w", err)
		}
	}
	return out, nil
}

// CompletenessHint returns the definitive pagination instruction, or Unknown
// when the relay omitted NIP-67 hints. If conflicting known hints are present,
// More wins so a client never silently truncates stored history.
func (e EOSE) CompletenessHint() Completeness {
	finish := false
	for _, hint := range e.Hints {
		switch hint {
		case HintMore:
			return More
		case HintFinish:
			finish = true
		}
	}
	if finish {
		return Complete
	}
	return Unknown
}
