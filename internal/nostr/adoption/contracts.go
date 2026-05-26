package adoption

import (
	"encoding/json"
	"time"

	"metiq/internal/commitments"
)

const (
	KindTeamPolicy          = 30382
	KindTrajectoryAudit     = 30383
	KindCommitmentSync      = 30384
	KindWorkerAdvertisement = 10100
	KindQACredentialLease   = 30385
	KindNodeCapability      = 30386
	KindSkillMarketplace    = 30387
)

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
	return EventFor(KindTeamPolicy, c.Namespace, c, []string{"t", "metiq-policy"})
}
func TrajectoryAuditEvent(c TrajectoryAuditContract) (DraftEvent, error) {
	return EventFor(KindTrajectoryAudit, c.SessionID, c, []string{"t", "metiq-trajectory"})
}
func CommitmentSyncContractFrom(c commitments.Commitment) CommitmentSyncContract {
	dueAt := int64(0)
	if !c.DueAt.IsZero() {
		dueAt = c.DueAt.Unix()
	}
	return CommitmentSyncContract{CommitmentID: c.ID, Kind: string(c.Kind), Status: string(c.Status), DueAt: dueAt}
}

func CommitmentSyncEvent(c CommitmentSyncContract) (DraftEvent, error) {
	return EventFor(KindCommitmentSync, c.CommitmentID, c, []string{"t", "metiq-commitment"})
}

func CommitmentEventFromTracked(c commitments.Commitment) (DraftEvent, error) {
	return CommitmentSyncEvent(CommitmentSyncContractFrom(c))
}
func WorkerAdvertisementEvent(c WorkerAdvertisementContract) (DraftEvent, error) {
	return EventFor(KindWorkerAdvertisement, c.WorkerID, c, []string{"t", "loom-worker"})
}
func QACredentialLeaseEvent(c QACredentialLeaseContract) (DraftEvent, error) {
	return EventFor(KindQACredentialLease, c.LeaseID, c, []string{"t", "qa-credential-lease"})
}
func NodeCapabilityEvent(c NodeCapabilityContract) (DraftEvent, error) {
	return EventFor(KindNodeCapability, c.NodeID, c, []string{"t", "metiq-node"})
}
func SkillMarketplaceEvent(c SkillMarketplaceContract) (DraftEvent, error) {
	return EventFor(KindSkillMarketplace, c.SkillID, c, []string{"t", "metiq-skill"})
}
