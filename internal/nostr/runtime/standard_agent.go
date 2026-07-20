package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/nostr/events"
)

// AgentSessionMode is the standard-agent data-access boundary for a session.
// Private sessions may read a private corpus but must not publish to fleet
// relays. Fleet sessions may publish fleet records but must not have a route
// to the private corpus.
type AgentSessionMode string

const (
	AgentSessionModeFleet   AgentSessionMode = "fleet"
	AgentSessionModePrivate AgentSessionMode = "private"
)

func normalizeAgentSessionMode(mode AgentSessionMode) AgentSessionMode {
	if strings.EqualFold(strings.TrimSpace(string(mode)), string(AgentSessionModePrivate)) {
		return AgentSessionModePrivate
	}
	return AgentSessionModeFleet
}

// AllowsFleetPublish reports whether this session may write to fleet relays.
func (mode AgentSessionMode) AllowsFleetPublish() bool {
	return normalizeAgentSessionMode(mode) == AgentSessionModeFleet
}

// AllowsPrivateCorpus reports whether this session may read private corpus data.
func (mode AgentSessionMode) AllowsPrivateCorpus() bool {
	return normalizeAgentSessionMode(mode) == AgentSessionModePrivate
}

func requireFleetSession(mode AgentSessionMode, action string) error {
	if !mode.AllowsFleetPublish() {
		return fmt.Errorf("%s: private session cannot publish to fleet relays", action)
	}
	return nil
}

// WorkerAdvertisement is the standard kind:10100 worker-ad shape for agents
// that expose worker capacity.
type WorkerAdvertisement struct {
	SessionMode       AgentSessionMode `json:"session_mode,omitempty"`
	AgentID           string           `json:"agent_id,omitempty"`
	Runtime           string           `json:"runtime,omitempty"`
	RuntimeVersion    string           `json:"runtime_version,omitempty"`
	Status            string           `json:"status,omitempty"`
	Capabilities      []string         `json:"capabilities,omitempty"`
	Tools             []string         `json:"tools,omitempty"`
	ContextVMFeatures []string         `json:"contextvm_features,omitempty"`
	Relays            []string         `json:"relays,omitempty"`
	Metadata          map[string]any   `json:"metadata,omitempty"`
}

func normalizeWorkerAdvertisement(in WorkerAdvertisement) WorkerAdvertisement {
	in.SessionMode = normalizeAgentSessionMode(in.SessionMode)
	in.AgentID = strings.TrimSpace(in.AgentID)
	in.Runtime = strings.TrimSpace(in.Runtime)
	if in.Runtime == "" {
		in.Runtime = "metiq"
	}
	in.RuntimeVersion = strings.TrimSpace(in.RuntimeVersion)
	in.Status = strings.ToLower(strings.TrimSpace(in.Status))
	if in.Status == "" {
		in.Status = "available"
	}
	in.Capabilities = normalizeCapabilityStrings(in.Capabilities)
	in.Tools = normalizeCapabilityStrings(in.Tools)
	in.ContextVMFeatures = normalizeCapabilityStrings(in.ContextVMFeatures)
	in.Relays = normalizeRelayURLs(in.Relays)
	return in
}

// BuildWorkerAdvertisementTags encodes the required CAS_WORKER_AD tags.
func BuildWorkerAdvertisementTags(ad WorkerAdvertisement) nostr.Tags {
	ad = normalizeWorkerAdvertisement(ad)
	tags := nostr.Tags{{"status", ad.Status}}
	tags = append(tags, []string{"session_mode", string(ad.SessionMode)})
	if ad.AgentID != "" {
		tags = append(tags, []string{"agent", ad.AgentID})
	}
	for _, cap := range ad.Capabilities {
		tags = append(tags, []string{"cap", cap})
	}
	for _, tool := range ad.Tools {
		tags = append(tags, []string{"tool", tool})
	}
	for _, feature := range ad.ContextVMFeatures {
		tags = append(tags, []string{"contextvm_feature", feature})
	}
	if ad.Runtime != "" {
		tag := []string{"runtime", ad.Runtime}
		if ad.RuntimeVersion != "" {
			tag = append(tag, ad.RuntimeVersion)
		}
		tags = append(tags, tag)
	}
	for _, relay := range ad.Relays {
		tags = append(tags, []string{"relay", relay})
	}
	return tags
}

// BuildWorkerAdvertisementContent encodes the worker-ad JSON payload.
func BuildWorkerAdvertisementContent(ad WorkerAdvertisement) string {
	ad = normalizeWorkerAdvertisement(ad)
	raw, err := json.Marshal(ad)
	if err != nil {
		return ""
	}
	return string(raw)
}

// PublishWorkerAdvertisement signs and publishes the standard kind:10100 worker advertisement.
func PublishWorkerAdvertisement(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, publishRelays []string, ad WorkerAdvertisement) (string, error) {
	ad = normalizeWorkerAdvertisement(ad)
	if err := requireFleetSession(ad.SessionMode, "publish worker ad"); err != nil {
		return "", err
	}
	if pool == nil {
		return "", fmt.Errorf("publish worker ad: pool is required")
	}
	if keyer == nil {
		return "", fmt.Errorf("publish worker ad: keyer is required")
	}
	relays := normalizeRelayURLs(publishRelays)
	if len(relays) == 0 {
		return "", fmt.Errorf("publish worker ad: at least one relay is required")
	}
	evt := nostr.Event{
		Kind:      nostr.Kind(events.CAS_WORKER_AD),
		CreatedAt: nostr.Now(),
		Tags:      BuildWorkerAdvertisementTags(ad),
		Content:   BuildWorkerAdvertisementContent(ad),
	}
	if err := keyer.SignEvent(ctx, &evt); err != nil {
		return "", fmt.Errorf("publish worker ad: sign event: %w", err)
	}
	pubCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	published := false
	var lastErr error
	for result := range pool.PublishMany(pubCtx, relays, evt) {
		if result.Error == nil {
			published = true
			continue
		}
		lastErr = result.Error
	}
	if !published {
		if lastErr == nil {
			lastErr = fmt.Errorf("no relays accepted the event")
		}
		return "", fmt.Errorf("publish worker ad: %w", lastErr)
	}
	return evt.ID.Hex(), nil
}

// AuditRecord is the standard kind:4903 CAS_AUDIT payload.
type AuditRecord struct {
	SessionMode AgentSessionMode `json:"session_mode,omitempty"`
	Actor       string           `json:"actor"`
	Action      string           `json:"action"`
	Target      string           `json:"target"`
	Outcome     string           `json:"outcome"`
	Correlation string           `json:"correlation,omitempty"`
	AgentID     string           `json:"agent_id,omitempty"`
	Details     map[string]any   `json:"details,omitempty"`
}

func normalizeAuditRecord(in AuditRecord) AuditRecord {
	in.SessionMode = normalizeAgentSessionMode(in.SessionMode)
	in.Actor = strings.TrimSpace(in.Actor)
	in.Action = strings.TrimSpace(in.Action)
	in.Target = strings.TrimSpace(in.Target)
	in.Outcome = strings.ToLower(strings.TrimSpace(in.Outcome))
	in.Correlation = strings.TrimSpace(in.Correlation)
	in.AgentID = strings.TrimSpace(in.AgentID)
	return in
}

// BuildAuditTags encodes required CAS_AUDIT tags plus correlation tags.
func BuildAuditTags(record AuditRecord) nostr.Tags {
	record = normalizeAuditRecord(record)
	tags := nostr.Tags{
		{"domain", "agent-runtime"},
		{"type", "CAS_AUDIT"},
		{"schema", "cascadia.audit.v1"},
		{"actor", record.Actor},
		{"action", record.Action},
		{"target", record.Target},
		{"outcome", record.Outcome},
	}
	tags = append(tags, []string{"session_mode", string(record.SessionMode)})
	if record.Correlation != "" {
		tags = append(tags, []string{"correlation", record.Correlation})
	}
	if record.AgentID != "" {
		tags = append(tags, []string{"agent", record.AgentID})
	}
	return tags
}

// BuildAuditContent encodes the standard audit payload.
func BuildAuditContent(record AuditRecord) string {
	record = normalizeAuditRecord(record)
	raw, err := json.Marshal(record)
	if err != nil {
		return ""
	}
	return string(raw)
}

// PublishAuditRecord signs and publishes a kind:4903 CAS_AUDIT record for consequential actions.
func PublishAuditRecord(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, publishRelays []string, record AuditRecord) (string, error) {
	record = normalizeAuditRecord(record)
	if err := requireFleetSession(record.SessionMode, "publish audit"); err != nil {
		return "", err
	}
	if pool == nil {
		return "", fmt.Errorf("publish audit: pool is required")
	}
	if keyer == nil {
		return "", fmt.Errorf("publish audit: keyer is required")
	}
	if record.Actor == "" || record.Action == "" || record.Target == "" || record.Outcome == "" {
		return "", fmt.Errorf("publish audit: actor, action, target, and outcome are required")
	}
	relays := normalizeRelayURLs(publishRelays)
	if len(relays) == 0 {
		return "", fmt.Errorf("publish audit: at least one relay is required")
	}
	evt := nostr.Event{
		Kind:      nostr.Kind(events.CAS_AUDIT),
		CreatedAt: nostr.Now(),
		Tags:      BuildAuditTags(record),
		Content:   BuildAuditContent(record),
	}
	if err := keyer.SignEvent(ctx, &evt); err != nil {
		return "", fmt.Errorf("publish audit: sign event: %w", err)
	}
	pubCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	published := false
	var lastErr error
	for result := range pool.PublishMany(pubCtx, relays, evt) {
		if result.Error == nil {
			published = true
			continue
		}
		lastErr = result.Error
	}
	if !published {
		if lastErr == nil {
			lastErr = fmt.Errorf("no relays accepted the event")
		}
		return "", fmt.Errorf("publish audit: %w", lastErr)
	}
	return evt.ID.Hex(), nil
}
