package adoption

import (
	"encoding/json"
	"time"

	"metiq/internal/commitments"
	"metiq/internal/nostr/events"
)

const (
	KindTeamPolicy          = int(events.CAS_CP_STATE)
	KindTrajectoryAudit     = int(events.CAS_AUDIT)
	KindCommitmentSync      = int(events.CAS_CP_STATE)
	KindWorkerAdvertisement = int(events.CAS_WORKER_AD)
	KindQACredentialLease   = int(events.CAS_CP_STATE)
	KindNodeCapability      = 30004
	KindSkillMarketplace    = 30387
)

const (
	// DTagPatternTeamPolicy identifies team policy state in CAS_CP_STATE events.
	DTagPatternTeamPolicy = "policy:team:<namespace>"
	// DTagPatternCommitmentSync identifies commitment sync state in CAS_CP_STATE events.
	DTagPatternCommitmentSync = "commitment:<commitment_id>"
	// DTagPatternQACredentialLease identifies QA credential leases in CAS_CP_STATE events.
	DTagPatternQACredentialLease = "lease:qa:<lease_id>"
	// DTagPatternTrajectoryAudit identifies trajectory audit summaries in CAS_AUDIT events.
	DTagPatternTrajectoryAudit = "adoption:trajectory-audit:<session_id>"
	// DTagPatternNodeCapability identifies node capability curation sets in NIP-51 kind 30004 events.
	DTagPatternNodeCapability = "capabilities:<node_id>"
	// DTagPatternWorkerAdvertisement identifies worker advertisements in CAS_WORKER_AD events.
	DTagPatternWorkerAdvertisement = "worker:<worker_id>"
	// DTagPatternSkillMarketplace identifies skill marketplace entries while this contract remains on its legacy kind.
	DTagPatternSkillMarketplace = "skill:<skill_id>"
)

const (
	SchemaTeamPolicy          = "cascadia.team.policy.v1"
	SchemaTrajectoryAudit     = "cascadia.trajectory.audit.v1"
	SchemaCommitmentSync      = "cascadia.commitment.sync.v1"
	SchemaWorkerAdvertisement = "cascadia.worker.advertisement.v1"
	SchemaQACredentialLease   = "cascadia.qa.credential.lease.v1"
	SchemaNodeCapability      = "cascadia.node.capability.v1"
	SchemaSkillMarketplace    = "cascadia.skill.marketplace.v1"
)

func TeamPolicyDTag(namespace string) string { return "policy:team:" + namespace }
func TrajectoryAuditDTag(sessionID string) string {
	return "adoption:trajectory-audit:" + sessionID
}
func CommitmentSyncDTag(commitmentID string) string {
	return "commitment:" + commitmentID
}
func WorkerAdvertisementDTag(workerID string) string { return "worker:" + workerID }
func QACredentialLeaseDTag(leaseID string) string {
	return "lease:qa:" + leaseID
}
func NodeCapabilityDTag(nodeID string) string { return "capabilities:" + nodeID }
func SkillMarketplaceDTag(skillID string) string {
	return "skill:" + skillID
}

type DraftEvent struct {
	Kind      int        `json:"kind"`
	Tags      [][]string `json:"tags"`
	Content   string     `json:"content"`
	CreatedAt int64      `json:"created_at"`
}

type TeamPolicyContract struct {
	Version     string               `json:"version"`
	Namespace   string               `json:"namespace"`
	Rules       []PolicyRuleContract `json:"rules"`
	GeneratedAt int64                `json:"generated_at,omitempty"`
	Hash        string               `json:"hash,omitempty"`
}

type PolicyRuleContract struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Source string `json:"source,omitempty"`
}
type TrajectoryAuditContract struct {
	SessionID   string         `json:"session_id"`
	SummaryHash string         `json:"summary_hash,omitempty"`
	EventCounts map[string]int `json:"event_counts"`
	Errors      int            `json:"errors"`
	Warnings    int            `json:"warnings"`
}
type CommitmentSyncContract struct {
	CommitmentID string `json:"commitment_id"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	DueAt        int64  `json:"due_at,omitempty"`
}
type WorkerAdvertisementContract struct {
	WorkerID     string   `json:"worker_id"`
	Models       []string `json:"models"`
	Capabilities []string `json:"capabilities"`
	RelayHints   []string `json:"relay_hints,omitempty"`
}
type QACredentialLeaseContract struct {
	LeaseID   string   `json:"lease_id"`
	Mint      string   `json:"mint,omitempty"`
	Scope     []string `json:"scope"`
	ExpiresAt int64    `json:"expires_at"`
}
type NodeCapabilityContract struct {
	NodeID       string   `json:"node_id"`
	Capabilities []string `json:"capabilities"`
	InvokeKinds  []int    `json:"invoke_kinds,omitempty"`
	RelayHints   []string `json:"relay_hints,omitempty"`
}
type SkillMarketplaceContract struct {
	SkillID string `json:"skill_id"`
	Version string `json:"version"`
	Name    string `json:"name"`
	Hash    string `json:"hash,omitempty"`
	License string `json:"license,omitempty"`
}

type ASTAnalyzerContract struct {
	Name             string   `json:"name"`
	PackagePatterns  []string `json:"package_patterns"`
	ForbiddenCalls   []string `json:"forbidden_calls"`
	RequiredHandlers []string `json:"required_handlers"`
}

func EventFor(kind int, d string, content any, tags ...[]string) (DraftEvent, error) {
	raw, err := json.Marshal(content)
	if err != nil {
		return DraftEvent{}, err
	}
	all := [][]string{{"d", d}, {"client", "metiq"}}
	all = append(all, tags...)
	return DraftEvent{Kind: kind, CreatedAt: time.Now().Unix(), Tags: all, Content: string(raw)}, nil
}

func TeamPolicyEvent(c TeamPolicyContract) (DraftEvent, error) {
	return EventFor(KindTeamPolicy, TeamPolicyDTag(c.Namespace), c, []string{"schema", SchemaTeamPolicy}, []string{"t", "metiq-policy"})
}
func TrajectoryAuditEvent(c TrajectoryAuditContract) (DraftEvent, error) {
	return EventFor(KindTrajectoryAudit, TrajectoryAuditDTag(c.SessionID), c, []string{"schema", SchemaTrajectoryAudit}, []string{"domain", "agent"}, []string{"type", "trajectory"}, []string{"t", "metiq-trajectory"})
}
func CommitmentSyncContractFrom(c commitments.Commitment) CommitmentSyncContract {
	dueAt := int64(0)
	if !c.DueAt.IsZero() {
		dueAt = c.DueAt.Unix()
	}
	return CommitmentSyncContract{CommitmentID: c.ID, Kind: string(c.Kind), Status: string(c.Status), DueAt: dueAt}
}

func CommitmentSyncEvent(c CommitmentSyncContract) (DraftEvent, error) {
	return EventFor(KindCommitmentSync, CommitmentSyncDTag(c.CommitmentID), c, []string{"schema", SchemaCommitmentSync}, []string{"t", "metiq-commitment"})
}

func CommitmentEventFromTracked(c commitments.Commitment) (DraftEvent, error) {
	return CommitmentSyncEvent(CommitmentSyncContractFrom(c))
}
func WorkerAdvertisementEvent(c WorkerAdvertisementContract) (DraftEvent, error) {
	return EventFor(KindWorkerAdvertisement, WorkerAdvertisementDTag(c.WorkerID), c, []string{"schema", SchemaWorkerAdvertisement}, []string{"t", "loom-worker"})
}
func QACredentialLeaseEvent(c QACredentialLeaseContract) (DraftEvent, error) {
	return EventFor(KindQACredentialLease, QACredentialLeaseDTag(c.LeaseID), c, []string{"schema", SchemaQACredentialLease}, []string{"t", "qa-credential-lease"})
}
func NodeCapabilityEvent(c NodeCapabilityContract) (DraftEvent, error) {
	return EventFor(KindNodeCapability, NodeCapabilityDTag(c.NodeID), c, []string{"schema", SchemaNodeCapability}, []string{"t", "metiq-node"})
}
func SkillMarketplaceEvent(c SkillMarketplaceContract) (DraftEvent, error) {
	return EventFor(KindSkillMarketplace, SkillMarketplaceDTag(c.SkillID), c, []string{"schema", SchemaSkillMarketplace}, []string{"t", "metiq-skill"})
}
