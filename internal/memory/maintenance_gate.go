package memory

// maintenance_gate.go — short-lived, state-bound confirmation challenges for
// destructive memory maintenance ops (swarmstr-qc53.4 / swarmstr-dhzo).

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

const maintenanceConfirmationTTL = 5 * time.Minute

type maintenanceChallenge struct {
	operation    string
	scope        string
	stateVersion string
	expiresAt    time.Time
}

var maintenanceChallengeStore = struct {
	sync.Mutex
	byToken map[string]maintenanceChallenge
}{byToken: make(map[string]maintenanceChallenge)}

// MaintenanceConfirmation is the outcome of issuing or consuming a challenge.
type MaintenanceConfirmation struct {
	Operation    string `json:"operation"`
	Scope        string `json:"scope"`
	ConfirmToken string `json:"confirm_token"`
	StateVersion string `json:"state_version"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
	Confirmed    bool   `json:"confirmed"`
	Required     bool   `json:"confirmation_required"`
}

var (
	ErrMaintenanceConfirmationMismatch = fmt.Errorf("memory maintenance confirmation token mismatch")
	ErrMaintenanceConfirmationStale    = fmt.Errorf("memory maintenance confirmation state changed since preview")
	ErrMaintenanceConfirmationExpired  = fmt.Errorf("memory maintenance confirmation token expired")
)

// EvaluateMaintenanceConfirmation issues a random, short-lived challenge when
// provided is empty. A provided challenge is consumed exactly once and succeeds
// only when operation, scope, and the current state version still match preview.
func EvaluateMaintenanceConfirmation(operation, scope, stateVersion, provided string) (MaintenanceConfirmation, error) {
	operation = strings.TrimSpace(operation)
	scope = strings.TrimSpace(scope)
	stateVersion = strings.TrimSpace(stateVersion)
	provided = strings.TrimSpace(provided)
	now := time.Now().UTC()

	if provided == "" {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			return MaintenanceConfirmation{}, fmt.Errorf("generate maintenance confirmation: %w", err)
		}
		token := operation + "-" + hex.EncodeToString(buf)
		expiresAt := now.Add(maintenanceConfirmationTTL)
		maintenanceChallengeStore.Lock()
		for key, challenge := range maintenanceChallengeStore.byToken {
			if !challenge.expiresAt.After(now) {
				delete(maintenanceChallengeStore.byToken, key)
			}
		}
		maintenanceChallengeStore.byToken[token] = maintenanceChallenge{
			operation: operation, scope: scope, stateVersion: stateVersion, expiresAt: expiresAt,
		}
		maintenanceChallengeStore.Unlock()
		return MaintenanceConfirmation{
			Operation: operation, Scope: scope, ConfirmToken: token, StateVersion: stateVersion,
			ExpiresAt: expiresAt.Unix(), Required: true,
		}, nil
	}

	maintenanceChallengeStore.Lock()
	challenge, found := maintenanceChallengeStore.byToken[provided]
	delete(maintenanceChallengeStore.byToken, provided)
	maintenanceChallengeStore.Unlock()
	out := MaintenanceConfirmation{
		Operation: operation, Scope: scope, ConfirmToken: provided, StateVersion: stateVersion,
		ExpiresAt: challenge.expiresAt.Unix(),
	}
	if !found || challenge.operation != operation || challenge.scope != scope {
		return out, ErrMaintenanceConfirmationMismatch
	}
	if !challenge.expiresAt.After(now) {
		return out, ErrMaintenanceConfirmationExpired
	}
	if challenge.stateVersion != stateVersion {
		return out, ErrMaintenanceConfirmationStale
	}
	out.Confirmed = true
	return out, nil
}
