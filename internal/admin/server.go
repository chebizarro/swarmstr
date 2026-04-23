package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"metiq/internal/config"
	"metiq/internal/extensions/feishu"
	"metiq/internal/extensions/line"
	"metiq/internal/extensions/msteams"
	"metiq/internal/extensions/nextcloud"
	"metiq/internal/extensions/synology"
	"metiq/internal/extensions/telegram"
	"metiq/internal/extensions/whatsapp"
	"metiq/internal/extensions/zalo"
	"metiq/internal/gateway/methods"
	mcppkg "metiq/internal/mcp"
	"metiq/internal/memory"
	"metiq/internal/policy"
	"metiq/internal/store/state"
)

type StatusProvider struct {
	PubKey   string
	Relays   []string
	DMPolicy string
	Started  time.Time
}

type SendDMRequest struct {
	To   string `json:"to"`
	Text string `json:"text"`
}

type SessionView struct {
	Session    state.SessionDoc           `json:"session"`
	Transcript []state.TranscriptEntryDoc `json:"transcript"`
}

type contextKey string

const tokenAuthContextKey contextKey = "admin-token-authenticated"

type ServerOptions struct {
	Addr                        string
	Token                       string
	Status                      StatusProvider
	StatusDMPolicy              func() string
	StatusRelays                func() []string
	StatusMCP                   func() *mcppkg.TelemetrySnapshot
	SearchMemory                func(query string, limit int) []memory.IndexedMemory
	MemoryStats                 func() (count int, sessionCount int)
	GetCheckpoint               func(context.Context, string) (state.CheckpointDoc, error)
	StartAgent                  func(context.Context, methods.AgentRequest) (map[string]any, error)
	WaitAgent                   func(context.Context, methods.AgentWaitRequest) (map[string]any, error)
	AgentIdentity               func(context.Context, methods.AgentIdentityRequest) (map[string]any, error)
	GatewayIdentity             func(context.Context) (map[string]any, error)
	SendDM                      func(context.Context, string, string) error
	AbortChat                   func(context.Context, string) (int, error)
	GetSession                  func(context.Context, string) (state.SessionDoc, error)
	PutSession                  func(context.Context, string, state.SessionDoc) error
	ListSessions                func(context.Context, int) ([]state.SessionDoc, error)
	SessionStore                *state.SessionStore
	ListTranscript              func(context.Context, string, int) ([]state.TranscriptEntryDoc, error)
	SessionsPrune               func(context.Context, methods.SessionsPruneRequest) (map[string]any, error)
	TailLogs                    func(context.Context, int64, int, int) (map[string]any, error)
	ObserveRuntime              func(context.Context, methods.RuntimeObserveRequest) (map[string]any, error)
	ChannelsStatus              func(context.Context, methods.ChannelsStatusRequest) (map[string]any, error)
	ChannelsLogout              func(context.Context, string) (map[string]any, error)
	UsageStatus                 func(context.Context) (map[string]any, error)
	UsageCost                   func(context.Context, methods.UsageCostRequest) (map[string]any, error)
	GetList                     func(context.Context, string) (state.ListDoc, error)
	PutList                     func(context.Context, string, state.ListDoc) error
	ListAgents                  func(context.Context, methods.AgentsListRequest) (map[string]any, error)
	CreateAgent                 func(context.Context, methods.AgentsCreateRequest) (map[string]any, error)
	UpdateAgent                 func(context.Context, methods.AgentsUpdateRequest) (map[string]any, error)
	DeleteAgent                 func(context.Context, methods.AgentsDeleteRequest) (map[string]any, error)
	ListAgentFiles              func(context.Context, methods.AgentsFilesListRequest) (map[string]any, error)
	GetAgentFile                func(context.Context, methods.AgentsFilesGetRequest) (map[string]any, error)
	SetAgentFile                func(context.Context, methods.AgentsFilesSetRequest) (map[string]any, error)
	ListModels                  func(context.Context, methods.ModelsListRequest) (map[string]any, error)
	ToolsCatalog                func(context.Context, methods.ToolsCatalogRequest) (map[string]any, error)
	ToolsProfileGet             func(context.Context, methods.ToolsProfileGetRequest) (map[string]any, error)
	ToolsProfileSet             func(context.Context, methods.ToolsProfileSetRequest) (map[string]any, error)
	SkillsStatus                func(context.Context, methods.SkillsStatusRequest) (map[string]any, error)
	SkillsBins                  func(context.Context, methods.SkillsBinsRequest) (map[string]any, error)
	SkillsInstall               func(context.Context, methods.SkillsInstallRequest) (map[string]any, error)
	SkillsUpdate                func(context.Context, methods.SkillsUpdateRequest) (map[string]any, error)
	PluginsInstall              func(context.Context, methods.PluginsInstallRequest) (map[string]any, error)
	PluginsUninstall            func(context.Context, methods.PluginsUninstallRequest) (map[string]any, error)
	PluginsUpdate               func(context.Context, methods.PluginsUpdateRequest) (map[string]any, error)
	PluginsRegistryList         func(context.Context, methods.PluginsRegistryListRequest) (map[string]any, error)
	PluginsRegistryGet          func(context.Context, methods.PluginsRegistryGetRequest) (map[string]any, error)
	PluginsRegistrySearch       func(context.Context, methods.PluginsRegistrySearchRequest) (map[string]any, error)
	NodePairRequest             func(context.Context, methods.NodePairRequest) (map[string]any, error)
	NodePairList                func(context.Context, methods.NodePairListRequest) (map[string]any, error)
	NodePairApprove             func(context.Context, methods.NodePairApproveRequest) (map[string]any, error)
	NodePairReject              func(context.Context, methods.NodePairRejectRequest) (map[string]any, error)
	NodePairVerify              func(context.Context, methods.NodePairVerifyRequest) (map[string]any, error)
	NodeList                    func(context.Context, methods.NodeListRequest) (map[string]any, error)
	NodeDescribe                func(context.Context, methods.NodeDescribeRequest) (map[string]any, error)
	NodeRename                  func(context.Context, methods.NodeRenameRequest) (map[string]any, error)
	NodeCanvasCapabilityRefresh func(context.Context, methods.NodeCanvasCapabilityRefreshRequest) (map[string]any, error)
	DevicePairList              func(context.Context, methods.DevicePairListRequest) (map[string]any, error)
	DevicePairApprove           func(context.Context, methods.DevicePairApproveRequest) (map[string]any, error)
	DevicePairReject            func(context.Context, methods.DevicePairRejectRequest) (map[string]any, error)
	DevicePairRemove            func(context.Context, methods.DevicePairRemoveRequest) (map[string]any, error)
	DeviceTokenRotate           func(context.Context, methods.DeviceTokenRotateRequest) (map[string]any, error)
	DeviceTokenRevoke           func(context.Context, methods.DeviceTokenRevokeRequest) (map[string]any, error)
	NodeInvoke                  func(context.Context, methods.NodeInvokeRequest) (map[string]any, error)
	NodeEvent                   func(context.Context, methods.NodeEventRequest) (map[string]any, error)
	NodeResult                  func(context.Context, methods.NodeResultRequest) (map[string]any, error)
	NodePendingEnqueue          func(context.Context, methods.NodePendingEnqueueRequest) (map[string]any, error)
	NodePendingPull             func(context.Context, methods.NodePendingPullRequest) (map[string]any, error)
	NodePendingAck              func(context.Context, methods.NodePendingAckRequest) (map[string]any, error)
	NodePendingDrain            func(context.Context, methods.NodePendingDrainRequest) (map[string]any, error)
	CronList                    func(context.Context, methods.CronListRequest) (map[string]any, error)
	CronStatus                  func(context.Context, methods.CronStatusRequest) (map[string]any, error)
	CronAdd                     func(context.Context, methods.CronAddRequest) (map[string]any, error)
	CronUpdate                  func(context.Context, methods.CronUpdateRequest) (map[string]any, error)
	CronRemove                  func(context.Context, methods.CronRemoveRequest) (map[string]any, error)
	CronRun                     func(context.Context, methods.CronRunRequest) (map[string]any, error)
	CronRuns                    func(context.Context, methods.CronRunsRequest) (map[string]any, error)
	ExecApprovalsGet            func(context.Context, methods.ExecApprovalsGetRequest) (map[string]any, error)
	ExecApprovalsSet            func(context.Context, methods.ExecApprovalsSetRequest) (map[string]any, error)
	ExecApprovalsNodeGet        func(context.Context, methods.ExecApprovalsNodeGetRequest) (map[string]any, error)
	ExecApprovalsNodeSet        func(context.Context, methods.ExecApprovalsNodeSetRequest) (map[string]any, error)
	ExecApprovalRequest         func(context.Context, methods.ExecApprovalRequestRequest) (map[string]any, error)
	ExecApprovalWaitDecision    func(context.Context, methods.ExecApprovalWaitDecisionRequest) (map[string]any, error)
	ExecApprovalResolve         func(context.Context, methods.ExecApprovalResolveRequest) (map[string]any, error)
	SandboxRun                  func(context.Context, methods.SandboxRunRequest) (map[string]any, error)
	MCPList                     func(context.Context, methods.MCPListRequest) (map[string]any, error)
	MCPGet                      func(context.Context, methods.MCPGetRequest) (map[string]any, error)
	MCPPut                      func(context.Context, methods.MCPPutRequest) (map[string]any, error)
	MCPRemove                   func(context.Context, methods.MCPRemoveRequest) (map[string]any, error)
	MCPTest                     func(context.Context, methods.MCPTestRequest) (map[string]any, error)
	MCPReconnect                func(context.Context, methods.MCPReconnectRequest) (map[string]any, error)
	MCPAuthStart                func(context.Context, methods.MCPAuthStartRequest) (map[string]any, error)
	MCPAuthRefresh              func(context.Context, methods.MCPAuthRefreshRequest) (map[string]any, error)
	MCPAuthClear                func(context.Context, methods.MCPAuthClearRequest) (map[string]any, error)
	SecretsReload               func(context.Context, methods.SecretsReloadRequest) (map[string]any, error)
	SecretsResolve              func(context.Context, methods.SecretsResolveRequest) (map[string]any, error)
	WizardStart                 func(context.Context, methods.WizardStartRequest) (map[string]any, error)
	WizardNext                  func(context.Context, methods.WizardNextRequest) (map[string]any, error)
	WizardCancel                func(context.Context, methods.WizardCancelRequest) (map[string]any, error)
	WizardStatus                func(context.Context, methods.WizardStatusRequest) (map[string]any, error)
	UpdateRun                   func(context.Context, methods.UpdateRunRequest) (map[string]any, error)
	TalkConfig                  func(context.Context, methods.TalkConfigRequest) (map[string]any, error)
	TalkMode                    func(context.Context, methods.TalkModeRequest) (map[string]any, error)
	LastHeartbeat               func(context.Context, methods.LastHeartbeatRequest) (map[string]any, error)
	SetHeartbeats               func(context.Context, methods.SetHeartbeatsRequest) (map[string]any, error)
	Wake                        func(context.Context, methods.WakeRequest) (map[string]any, error)
	SystemPresence              func(context.Context, methods.SystemPresenceRequest) ([]map[string]any, error)
	SystemEvent                 func(context.Context, methods.SystemEventRequest) (map[string]any, error)
	Send                        func(context.Context, methods.SendRequest) (map[string]any, error)
	SendPoll                    func(context.Context, methods.PollRequest) (map[string]any, error)
	BrowserRequest              func(context.Context, methods.BrowserRequestRequest) (map[string]any, error)
	VoicewakeGet                func(context.Context, methods.VoicewakeGetRequest) (map[string]any, error)
	VoicewakeSet                func(context.Context, methods.VoicewakeSetRequest) (map[string]any, error)
	TTSStatus                   func(context.Context, methods.TTSStatusRequest) (map[string]any, error)
	TTSProviders                func(context.Context, methods.TTSProvidersRequest) (map[string]any, error)
	TTSSetProvider              func(context.Context, methods.TTSSetProviderRequest) (map[string]any, error)
	TTSEnable                   func(context.Context, methods.TTSEnableRequest) (map[string]any, error)
	TTSDisable                  func(context.Context, methods.TTSDisableRequest) (map[string]any, error)
	TTSConvert                  func(context.Context, methods.TTSConvertRequest) (map[string]any, error)
	GetConfig                   func(context.Context) (state.ConfigDoc, error)
	PutConfig                   func(context.Context, state.ConfigDoc) error
	ConfigSet                   func(context.Context, methods.ConfigSetRequest) (map[string]any, int, error)
	ConfigApply                 func(context.Context, methods.ConfigApplyRequest) (map[string]any, int, error)
	ConfigPatch                 func(context.Context, methods.ConfigPatchRequest) (map[string]any, int, error)
	GetListWithEvent            func(context.Context, string) (state.ListDoc, state.Event, error)
	GetConfigWithEvent          func(context.Context) (state.ConfigDoc, state.Event, error)
	GetRelayPolicy              func(context.Context) (methods.RelayPolicyResponse, error)
	SupportedMethods            func(context.Context) ([]string, error)
	DelegateControlCall         func(context.Context, string, json.RawMessage) (any, int, error)

	// EmbedTexts generates embeddings for a batch of input texts.
	// If nil, the /v1/embeddings endpoint returns 501.
	EmbedTexts EmbedFunc

	// MCPManager is the MCP server manager used for the loopback MCP server.
	// If nil, the /mcp endpoint is not mounted.
	MCPManager *mcppkg.Manager

	// Metrics is an optional callback that returns the Prometheus text
	// exposition for /metrics.  If nil the endpoint returns a minimal stub.
	Metrics func(context.Context) string
	// HealthExtra is an optional callback that adds extra fields to /healthz.
	HealthExtra func(context.Context) map[string]any
}

func Start(ctx context.Context, opts ServerOptions) error {
	if strings.TrimSpace(opts.Addr) == "" {
		return nil
	}
	if err := validateExposure(opts.Addr, opts.Token); err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/webhooks/msteams/", func(w http.ResponseWriter, r *http.Request) {
		channelID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/webhooks/msteams/"))
		if channelID == "" {
			http.Error(w, "missing channel id", http.StatusBadRequest)
			return
		}
		msteams.HandleWebhook(channelID, w, r)
	})
	mux.HandleFunc("/webhooks/synology/", func(w http.ResponseWriter, r *http.Request) {
		channelID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/webhooks/synology/"))
		if channelID == "" {
			http.Error(w, "missing channel id", http.StatusBadRequest)
			return
		}
		synology.HandleWebhook(channelID, w, r)
	})
	mux.HandleFunc("/webhooks/line/", func(w http.ResponseWriter, r *http.Request) {
		channelID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/webhooks/line/"))
		if channelID == "" {
			http.Error(w, "missing channel id", http.StatusBadRequest)
			return
		}
		line.HandleWebhook(channelID, w, r)
	})
	mux.HandleFunc("/webhooks/nextcloud/", func(w http.ResponseWriter, r *http.Request) {
		channelID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/webhooks/nextcloud/"))
		if channelID == "" {
			http.Error(w, "missing channel id", http.StatusBadRequest)
			return
		}
		nextcloud.HandleWebhook(channelID, w, r)
	})
	mux.HandleFunc("/webhooks/feishu/", func(w http.ResponseWriter, r *http.Request) {
		channelID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/webhooks/feishu/"))
		if channelID == "" {
			http.Error(w, "missing channel id", http.StatusBadRequest)
			return
		}
		feishu.HandleWebhook(channelID, w, r)
	})
	mux.HandleFunc("/webhooks/telegram/", func(w http.ResponseWriter, r *http.Request) {
		channelID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/webhooks/telegram/"))
		if channelID == "" {
			http.Error(w, "missing channel id", http.StatusBadRequest)
			return
		}
		telegram.HandleWebhook(channelID, w, r)
	})
	mux.HandleFunc("/webhooks/whatsapp/", func(w http.ResponseWriter, r *http.Request) {
		channelID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/webhooks/whatsapp/"))
		if channelID == "" {
			http.Error(w, "missing channel id", http.StatusBadRequest)
			return
		}
		whatsapp.HandleWebhook(channelID, w, r)
	})
	mux.HandleFunc("/webhooks/zalo/", func(w http.ResponseWriter, r *http.Request) {
		channelID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/webhooks/zalo/"))
		if channelID == "" {
			http.Error(w, "missing channel id", http.StatusBadRequest)
			return
		}
		zalo.HandleWebhook(channelID, w, r)
	})
	// HTTP webhook ingress — POST /hooks/{wake,agent,<mapped>}
	// Active only when hooks.enabled=true in runtime ConfigDoc.
	mountWebhookIngress(mux, opts)

	// OpenAI-compatible chat completions — POST /v1/chat/completions
	mountOpenAIChatCompletions(mux, opts)

	// OpenAI Responses API — POST /v1/responses
	mountOpenAIResponses(mux, opts)

	// OpenAI-compatible models — GET /v1/models, GET /v1/models/{id}
	mountOpenAIModels(mux, opts)

	// OpenAI-compatible embeddings — POST /v1/embeddings
	mountOpenAIEmbeddings(mux, opts)

	// MCP loopback server — POST /mcp (JSON-RPC 2.0)
	mountMCPLoopback(mux, opts)

	mux.HandleFunc("/health", withAuth(opts.Token, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	// /healthz — Kubernetes-style liveness probe (no auth required for ease of use
	// by orchestration systems).
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{"status": "ok"}
		if opts.HealthExtra != nil {
			for k, v := range opts.HealthExtra(r.Context()) {
				body[k] = v
			}
		}
		writeJSON(w, http.StatusOK, body)
	})
	// /metrics — Prometheus text exposition format.  Requires auth if token is set.
	mux.HandleFunc("/metrics", withAuth(opts.Token, func(w http.ResponseWriter, r *http.Request) {
		var exposition string
		if opts.Metrics != nil {
			exposition = opts.Metrics(r.Context())
		} else {
			exposition = "# metiqd metrics not configured\n"
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(exposition))
	}))
	mux.HandleFunc("/status", withAuth(opts.Token, func(w http.ResponseWriter, _ *http.Request) {
		dmPolicy := opts.Status.DMPolicy
		if opts.StatusDMPolicy != nil {
			dmPolicy = opts.StatusDMPolicy()
		}
		relays := opts.Status.Relays
		if opts.StatusRelays != nil {
			relays = opts.StatusRelays()
		}
		var mcp *mcppkg.TelemetrySnapshot
		if opts.StatusMCP != nil {
			mcp = opts.StatusMCP()
		}
		writeJSON(w, http.StatusOK, methods.StatusResponse{
			PubKey:        opts.Status.PubKey,
			Relays:        relays,
			DMPolicy:      dmPolicy,
			UptimeSeconds: int(time.Since(opts.Status.Started).Seconds()),
			UptimeMS:      time.Since(opts.Status.Started).Milliseconds(),
			Version:       "metiqd",
			MCP:           mcp,
		})
	}))
	mux.HandleFunc("/memory/search", withAuth(opts.Token, func(w http.ResponseWriter, r *http.Request) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing q"})
			return
		}
		if utf8.RuneCountInString(q) > 256 {
			q = truncateRunes(q, 256)
		}
		limit := parseLimit(r.URL.Query().Get("limit"), 20, 200)
		if opts.SearchMemory == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "memory search not configured"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": opts.SearchMemory(q, limit)})
	}))
	mux.HandleFunc("/checkpoints/", withAuth(opts.Token, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		name := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/checkpoints/"))
		if name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing checkpoint name"})
			return
		}
		if opts.GetCheckpoint == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "checkpoint provider not configured"})
			return
		}
		doc, err := opts.GetCheckpoint(r.Context(), name)
		if err != nil {
			handleStateError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, doc)
	}))
	mux.HandleFunc("/chat/send", withAuth(opts.Token, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		if opts.SendDM == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "send dm not configured"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var req SendDMRequest
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
			return
		}
		req.To = strings.TrimSpace(req.To)
		req.Text = strings.TrimSpace(req.Text)
		if req.To == "" || req.Text == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "to and text are required"})
			return
		}
		if err := opts.SendDM(r.Context(), req.To, req.Text); err != nil {
			log.Printf("admin send dm failed: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "send failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	mux.HandleFunc("/sessions/", withAuth(opts.Token, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		sessionID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/sessions/"))
		if sessionID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing session id"})
			return
		}
		if opts.GetSession == nil || opts.ListTranscript == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "session providers not configured"})
			return
		}
		limit := parseLimit(r.URL.Query().Get("limit"), 50, 500)
		session, err := opts.GetSession(r.Context(), sessionID)
		if err != nil {
			handleStateError(w, err)
			return
		}
		transcript, err := opts.ListTranscript(r.Context(), sessionID, limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, SessionView{Session: session, Transcript: transcript})
	}))
	mux.HandleFunc("/call", withAuth(opts.Token, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		result, status, err := dispatchMethodCall(r.Context(), w, r, opts)
		if isNIP86RPC(r) {
			if err != nil {
				writeNIP86JSON(w, map[string]any{"error": methods.MapNIP86Error(status, err)})
				return
			}
			writeNIP86JSON(w, map[string]any{"result": result})
			return
		}
		if err != nil {
			writeJSON(w, status, methods.CallResponse{OK: false, Error: err.Error()})
			return
		}
		writeJSON(w, status, methods.CallResponse{OK: true, Result: result})
	}))
	mux.HandleFunc("/config", withAuth(opts.Token, func(w http.ResponseWriter, r *http.Request) {
		if opts.GetConfig == nil || opts.PutConfig == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "config providers not configured"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			cfg, err := opts.GetConfig(r.Context())
			if err != nil {
				handleStateError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, config.Redact(cfg))
		case http.MethodPut:
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			dec := json.NewDecoder(r.Body)
			dec.DisallowUnknownFields()
			var cfg state.ConfigDoc
			if err := dec.Decode(&cfg); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
				return
			}
			if cfg.Version == 0 {
				cfg.Version = 1
			}
			if err := opts.PutConfig(r.Context(), cfg); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		}
	}))

	srv := &http.Server{
		Addr:              opts.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		// Agent waits, tool approvals, and live local-model turns can legitimately
		// exceed the default net/http server timeout budget. Keep the write window
		// long enough that /call can return completed agent results instead of
		// terminating the client connection with EOF mid-turn.
		WriteTimeout:   10 * time.Minute,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("admin API listening on %s", opts.Addr)
	err := srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func dispatchMethodCall(ctx context.Context, w http.ResponseWriter, r *http.Request, opts ServerOptions) (any, int, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("invalid request body")
	}
	var call methods.CallRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&call); err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("invalid request body")
	}
	method := strings.TrimSpace(call.Method)
	if method == "" {
		return nil, http.StatusBadRequest, fmt.Errorf("method is required")
	}
	method = canonicalMethodName(method)

	cfg := state.ConfigDoc{}
	if opts.GetConfig != nil {
		current, err := opts.GetConfig(ctx)
		if err != nil {
			log.Printf("admin /call: failed to load config, falling back to secure defaults: %v", err)
			cfg.Control.RequireAuth = true
		} else {
			cfg = current
		}
	}
	auth := policy.AuthenticateControlCall(r, raw, 30*time.Second)
	if !auth.Authenticated {
		if tokenOK, _ := ctx.Value(tokenAuthContextKey).(bool); tokenOK && cfg.Control.LegacyTokenFallback && len(cfg.Control.Admins) == 0 {
			auth.Authenticated = true
			auth.CallerPubKey = "token-local"
			auth.Reason = ""
			cfg.Control.RequireAuth = false
		}
	}
	ctx = context.WithValue(ctx, callerPubKeyContextKey, auth.CallerPubKey)
	decision := policy.EvaluateControlCall(auth.CallerPubKey, method, auth.Authenticated, cfg)
	if !decision.Allowed {
		if !decision.Authenticated {
			reason := decision.Reason
			if reason == "" {
				reason = auth.Reason
			}
			if strings.TrimSpace(reason) == "" {
				reason = "authentication required"
			}
			return nil, http.StatusUnauthorized, errors.New(reason)
		}
		if strings.TrimSpace(decision.Reason) == "" {
			return nil, http.StatusForbidden, fmt.Errorf("forbidden")
		}
		return nil, http.StatusForbidden, errors.New(decision.Reason)
	}

	switch {
	case methods.InAdminDispatchGroup(methods.AdminDispatchAgents, method):
		return dispatchAgents(ctx, opts, method, call, cfg)
	case methods.InAdminDispatchGroup(methods.AdminDispatchChannels, method):
		return dispatchChannels(ctx, opts, method, call, cfg)
	case methods.InAdminDispatchGroup(methods.AdminDispatchConfig, method):
		return dispatchConfig(ctx, opts, method, call, cfg)
	case methods.InAdminDispatchGroup(methods.AdminDispatchCron, method):
		return dispatchCron(ctx, opts, method, call, cfg)
	case methods.InAdminDispatchGroup(methods.AdminDispatchExec, method):
		return dispatchExec(ctx, opts, method, call, cfg)
	case methods.InAdminDispatchGroup(methods.AdminDispatchMCP, method):
		return dispatchMcp(ctx, opts, method, call, cfg)
	case methods.InAdminDispatchGroup(methods.AdminDispatchMedia, method):
		return dispatchMedia(ctx, opts, method, call, cfg)
	case methods.InAdminDispatchGroup(methods.AdminDispatchNodes, method):
		return dispatchNodes(ctx, opts, method, call, cfg)
	case methods.InAdminDispatchGroup(methods.AdminDispatchPlugins, method):
		return dispatchPlugins(ctx, opts, method, call, cfg)
	case methods.InAdminDispatchGroup(methods.AdminDispatchRuntime, method):
		return dispatchRuntime(ctx, opts, method, call, cfg)
	case methods.InAdminDispatchGroup(methods.AdminDispatchSessions, method):
		return dispatchSessions(ctx, opts, method, call, cfg)
	case methods.InAdminDispatchGroup(methods.AdminDispatchTasks, method):
		return dispatchTasks(ctx, opts, method, call, cfg)
	case methods.InAdminDispatchGroup(methods.AdminDispatchSystem, method):
		return dispatchSystem(ctx, opts, method, call, cfg)
	case methods.InAdminDispatchGroup(methods.AdminDispatchACP, method):
		return dispatchACP(ctx, opts, method, call, cfg)
	default:
		return nil, http.StatusNotFound, fmt.Errorf("unknown method %q", method)
	}
}
