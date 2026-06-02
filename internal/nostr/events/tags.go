package events

const (
	// ─────────────────────────────────────────────────────────────────────────
	// Standard NIP Tags
	// ─────────────────────────────────────────────────────────────────────────

	TagEventRef   = "e"          // NIP-01: Event reference
	TagPubkey     = "p"          // NIP-01: Pubkey reference
	TagDTag       = "d"          // NIP-01: Addressable event identifier
	TagKind       = "k"          // NIP-01: Kind reference
	TagExpiration = "expiration" // NIP-40: Event expiration timestamp

	// ─────────────────────────────────────────────────────────────────────────
	// Cascadia Routing Tags
	// Reference: cascadia-nips/docs/tags.md
	// ─────────────────────────────────────────────────────────────────────────

	// TagDomain is the operational domain (service, dns, ci, worker, etc).
	// Primary relay-level discriminator for control-plane events.
	TagDomain = "domain"

	// TagOp is the operation type (deploy, rollback, create, etc).
	// Combined with domain tag for full operation identification.
	TagOp = "op"

	// TagService is the target service identifier.
	TagService = "service"

	// TagEnvironment is the target environment identifier.
	TagEnvironment = "environment"

	// TagWorker is the target worker identifier.
	TagWorker = "worker"

	// TagAgent is the agent or service identifier.
	TagAgent = "agent"

	// TagRoute is the LLM/API route identifier.
	TagRoute = "route"

	// TagRelease is the release or version identifier.
	TagRelease = "release"

	// ─────────────────────────────────────────────────────────────────────────
	// Cascadia Correlation Tags
	// ─────────────────────────────────────────────────────────────────────────

	// TagSession is the session or conversation identifier.
	TagSession = "session"

	// TagRun is the execution run identifier.
	TagRun = "run"

	// TagIntent is the deployment intent identifier.
	TagIntent = "intent"

	// ─────────────────────────────────────────────────────────────────────────
	// Cascadia Lineage Tags
	// ─────────────────────────────────────────────────────────────────────────

	// TagSchema is the payload schema identifier.
	// Format: <system>.<entity>.<version>
	TagSchema = "schema"

	// TagType is the document/event type discriminator.
	// Used for type discrimination within shared kinds (e.g., NIP-78).
	TagType = "type"

	// TagArtifact is the artifact reference (name:version or free-form).
	TagArtifact = "artifact"

	// TagProject is the project identifier.
	TagProject = "project"

	// TagWorkflow is the workflow identifier.
	TagWorkflow = "workflow"

	// TagVersion is the content version counter.
	TagVersion = "v"

	// TagSupersedes is the coordinate of the superseded event.
	TagSupersedes = "supersedes"

	// ─────────────────────────────────────────────────────────────────────────
	// Cascadia State Tags
	// ─────────────────────────────────────────────────────────────────────────

	// TagStatus is the current or terminal status of an operation.
	TagStatus = "status"

	// TagStage is the workflow stage (implement, review, deploy, verify).
	TagStage = "stage"

	// TagStep is the execution step within a stage.
	TagStep = "step"

	// TagDecision is the decision outcome (approved, rejected).
	TagDecision = "decision"

	// ─────────────────────────────────────────────────────────────────────────
	// Cascadia Discovery Tags
	// ─────────────────────────────────────────────────────────────────────────

	// TagCap is a capability identifier (multi-value).
	TagCap = "cap"

	// TagRuntime is the runtime environment identifier.
	TagRuntime = "runtime"

	// TagModel is the AI/ML model identifier.
	TagModel = "model"

	// TagTools is available tool names (multi-value).
	TagTools = "tools"

	// TagTool is a specific tool name for individual invocations.
	TagTool = "tool"

	// TagBackend is the backend type or identifier.
	TagBackend = "backend"

	// TagScope is the access or operation scope.
	TagScope = "scope"

	// TagDMSchemes is supported DM encryption schemes.
	TagDMSchemes = "dm_schemes"

	// TagACPVersion is the Agent Communication Protocol version.
	TagACPVersion = "acp_version"

	// TagContextVMFeatures lists supported ContextVM features.
	TagContextVMFeatures = "contextvm_features"

	// ─────────────────────────────────────────────────────────────────────────
	// Application Tags
	// ─────────────────────────────────────────────────────────────────────────

	// TagClient is the client application identifier.
	TagClient = "client"

	// TagRelay is a relay URL hint.
	TagRelay = "relay"

	// TagTopic is a semantic topic tag.
	TagTopic = "topic"

	// TagKeyword is a search keyword tag.
	TagKeyword = "keyword"

	// TagSource is the source identifier.
	TagSource = "source"

	// TagGoal is the goal or objective.
	TagGoal = "goal"

	// TagRef is a general reference tag.
	TagRef = "ref"

	// TagRole is a role identifier.
	TagRole = "role"

	// ─────────────────────────────────────────────────────────────────────────
	// Memory-Specific Tags
	// ─────────────────────────────────────────────────────────────────────────

	TagMemType   = "mem_type"
	TagMemTaskID = "task_id"
	TagMemSource = "mem_source"

	// ─────────────────────────────────────────────────────────────────────────
	// Feedback/Proposal Tags (Swarmstr-specific)
	// ─────────────────────────────────────────────────────────────────────────

	TagFeedback         = "feedback"
	TagFeedbackSource   = "fb_source"
	TagFeedbackSeverity = "fb_severity"
	TagFeedbackCategory = "fb_category"
	TagStepID           = "step_id"
	TagProposal         = "proposal"
	TagProposalKind     = "prop_kind"
	TagProposalStatus   = "prop_status"
	TagRetro            = "retro"
	TagRetroTrigger     = "retro_trigger"
	TagRetroOutcome     = "retro_outcome"

	// ─────────────────────────────────────────────────────────────────────────
	// Deprecated Aliases
	// These are preserved for backward compatibility. Use canonical names above.
	// ─────────────────────────────────────────────────────────────────────────

	// TagRecipient is DEPRECATED. Use TagPubkey instead.
	TagRecipient = TagPubkey

	// TagDedupe is DEPRECATED. Use TagDTag instead.
	TagDedupe = TagDTag

	// TagTaskID is DEPRECATED. Use TagTopic for NIP-12 compatibility.
	TagTaskID = "t"

	// TagRunID is DEPRECATED. Use TagRun instead.
	TagRunID = TagRun
)
