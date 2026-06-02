package events

// ─────────────────────────────────────────────────────────────────────────────
// Cascadia Schema Constants
// Reference: cascadia-nips/registry/event-families.yaml
// ─────────────────────────────────────────────────────────────────────────────

const (
	// Agent Lifecycle Schemas
	SchemaCascadiaAgentHeartbeatV1  = "cascadia.agent.heartbeat.v1"
	SchemaCascadiaAgentCapabilityV1 = "cascadia.agent.capability.v1"

	// Worker Schemas
	SchemaCascadiaLoomWorkerV1 = "cascadia.loom.worker.v1"

	// Control-Plane Schemas
	SchemaCascadiaLoomJobV1 = "cascadia.loom-job.v1"
	SchemaCascadiaSoulStateV1 = "cascadia.soul-state.v1"

	// Audit Schemas
	SchemaCascadiaAuditBuildV1      = "cascadia.audit.build.v1"
	SchemaCascadiaAuditDeploymentV1 = "cascadia.audit.deployment.v1"
	SchemaCascadiaAuditAgentV1      = "cascadia.audit.agent.v1"

	// Intent Schemas
	SchemaCascadiaIntentDeployV1  = "cascadia.intent.deploy.v1"
	SchemaCascadiaIntentAgentV1   = "cascadia.intent.agent.v1"
	SchemaCascadiaIntentWorkerV1  = "cascadia.intent.worker.v1"
	SchemaCascadiaIntentApprovalV1 = "cascadia.intent.approval.v1"
)

// ─────────────────────────────────────────────────────────────────────────────
// NIP-78 App Data Type Constants
// Used with TagType for discrimination within KindAppData (30078).
// ─────────────────────────────────────────────────────────────────────────────

const (
	// AppDataTypeTranscript discriminates transcript entries in NIP-78.
	AppDataTypeTranscript = "transcript"

	// AppDataTypeMemory discriminates memory documents in NIP-78.
	AppDataTypeMemory = "memory"

	// AppDataTypeState discriminates generic state documents in NIP-78.
	AppDataTypeState = "state"

	// AppDataTypeConfig discriminates configuration documents in NIP-78.
	AppDataTypeConfig = "config"

	// AppDataTypePrefs discriminates preference documents in NIP-78.
	AppDataTypePrefs = "prefs"
)

// ─────────────────────────────────────────────────────────────────────────────
// NIP-38 Status Category Constants
// Reference: cascadia-nips/docs/nostr-native-event-strategy.md §10
// ─────────────────────────────────────────────────────────────────────────────

const (
	// StatusCategoryGeneral is the default NIP-38 status category.
	StatusCategoryGeneral = "general"

	// StatusCategoryCascadiaAgent is the Cascadia agent status category.
	StatusCategoryCascadiaAgent = "cascadia:agent"

	// StatusCategoryCascadiaWorker is the Cascadia worker status category.
	StatusCategoryCascadiaWorker = "cascadia:worker"

	// StatusCategoryCascadiaService is the Cascadia service status category.
	StatusCategoryCascadiaService = "cascadia:service"

	// StatusCategoryCascadiaDeployment is the Cascadia deployment status category.
	StatusCategoryCascadiaDeployment = "cascadia:deployment"
)

// ─────────────────────────────────────────────────────────────────────────────
// NIP-38 Status Value Constants
// ─────────────────────────────────────────────────────────────────────────────

// Agent status values
const (
	StatusAgentOnline   = "online"
	StatusAgentBusy     = "busy"
	StatusAgentPaused   = "paused"
	StatusAgentDraining = "draining"
	StatusAgentOffline  = "offline"
	StatusAgentError    = "error"
)

// Worker status values
const (
	StatusWorkerAvailable   = "available"
	StatusWorkerBusy        = "busy"
	StatusWorkerCordoned    = "cordoned"
	StatusWorkerDraining    = "draining"
	StatusWorkerMaintenance = "maintenance"
	StatusWorkerOffline     = "offline"
)

// Service status values
const (
	StatusServiceHealthy   = "healthy"
	StatusServiceDegraded  = "degraded"
	StatusServiceUnhealthy = "unhealthy"
	StatusServiceDeploying = "deploying"
	StatusServiceRollback  = "rollback"
	StatusServiceOffline   = "offline"
)

// ─────────────────────────────────────────────────────────────────────────────
// Client Identifiers
// ─────────────────────────────────────────────────────────────────────────────

const (
	// ClientMetiq is the metiq client identifier.
	ClientMetiq = "metiq"

	// ClientSwarmstr is the swarmstr client identifier.
	ClientSwarmstr = "swarmstr"
)

// ─────────────────────────────────────────────────────────────────────────────
// ContextVM Method Names
// Reference: cascadia-nips/docs/nostr-native-event-strategy.md §4.5
// ─────────────────────────────────────────────────────────────────────────────

const (
	// Agent methods
	MethodAgentSpawn     = "agent/spawn"
	MethodAgentKill      = "agent/kill"
	MethodAgentPause     = "agent/pause"
	MethodAgentResume    = "agent/resume"
	MethodAgentConfigure = "agent/configure"
	MethodAgentUpgrade   = "agent/upgrade"

	// Service methods
	MethodServiceDeploy   = "service/deploy"
	MethodServiceRollback = "service/rollback"
	MethodServiceScale    = "service/scale"
	MethodServiceRestart  = "service/restart"
	MethodServiceStop     = "service/stop"
	MethodServiceUpdate   = "service/update"
	MethodServiceDelete   = "service/delete"

	// Worker methods
	MethodWorkerCordon          = "worker/cordon"
	MethodWorkerUncordon        = "worker/uncordon"
	MethodWorkerDrain           = "worker/drain"
	MethodWorkerUndrain         = "worker/undrain"
	MethodWorkerMaintenanceEnter = "worker/maintenance-enter"
	MethodWorkerMaintenanceExit  = "worker/maintenance-exit"

	// Approval methods
	MethodApprovalRequest = "approval/request"
	MethodApprovalApprove = "approval/approve"
	MethodApprovalReject  = "approval/reject"

	// CI methods
	MethodCIWorkflowRun = "ci/workflow-run"
	MethodCICancel      = "ci/cancel"
	MethodCIRetry       = "ci/retry"

	// MCP standard methods
	MethodToolsList  = "tools/list"
	MethodToolsCall  = "tools/call"
	MethodResourcesList = "resources/list"
	MethodResourcesRead = "resources/read"
	MethodPromptsList   = "prompts/list"
	MethodPromptsGet    = "prompts/get"
)

// ─────────────────────────────────────────────────────────────────────────────
// JSON-RPC Error Codes
// Reference: cascadia-nips/docs/nostr-native-event-strategy.md §4.7
// ─────────────────────────────────────────────────────────────────────────────

const (
	RPCErrorParseError     = -32700 // Malformed JSON
	RPCErrorInvalidRequest = -32600 // Missing required fields
	RPCErrorMethodNotFound = -32601 // Unknown domain/operation
	RPCErrorInvalidParams  = -32602 // Schema validation failed
	RPCErrorUnauthorized   = -32001 // Invalid signature or unknown pubkey
	RPCErrorForbidden      = -32002 // Pubkey not in ACL
	RPCErrorNotFound       = -32003 // Target entity doesn't exist
	RPCErrorConflict       = -32004 // Idempotency collision or state conflict
	RPCErrorExpired        = -32005 // Request past expiration
	RPCErrorRateLimited    = -32006 // Too many requests
)
