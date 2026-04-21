package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"metiq/internal/admin"
	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

type controlTransportParityFixture struct {
	Cases []struct {
		Name                string         `json:"name"`
		Method              string         `json:"method"`
		Params              map[string]any `json:"params"`
		Scenario            string         `json:"scenario"`
		ExpectErrorContains string         `json:"expect_error_contains"`
		IgnoreResultKeys    []string       `json:"ignore_result_keys"`
		IgnoreResultPaths   []string       `json:"ignore_result_paths"`
	} `json:"cases"`
}

type controlTransportParityHarness struct {
	baseURL        string
	client         *http.Client
	dmBus          nostruntime.DMTransport
	cfgState       *runtimeConfigStore
	docsRepo       *state.DocsRepository
	transcriptRepo *state.TranscriptRepository
	sessionStore   *state.SessionStore
	tools          *agent.ToolRegistry
	startedAt      time.Time
}

type parityDMTransport struct {
	pubkey string
	relays []string
}

func (p parityDMTransport) SendDM(context.Context, string, string) error {
	return fmt.Errorf("send runtime not configured")
}
func (p parityDMTransport) PublicKey() string { return p.pubkey }
func (p parityDMTransport) Relays() []string  { return append([]string{}, p.relays...) }
func (p *parityDMTransport) SetRelays(relays []string) error {
	p.relays = append([]string{}, relays...)
	return nil
}
func (p parityDMTransport) Close() {}

func TestParityDMTransportSetRelaysMutatesReceiver(t *testing.T) {
	bus := &parityDMTransport{pubkey: "parity-pubkey", relays: []string{"wss://old"}}
	if err := bus.SetRelays([]string{"wss://new-a", "wss://new-b"}); err != nil {
		t.Fatalf("SetRelays: %v", err)
	}
	got := bus.Relays()
	if len(got) != 2 || got[0] != "wss://new-a" || got[1] != "wss://new-b" {
		t.Fatalf("relays = %#v", got)
	}
}

func TestControlTransportParityMatrixFixtures(t *testing.T) {
	var fx controlTransportParityFixture
	loadJSONFixture(t, filepath.Join("testdata", "parity", "control_transport_matrix.json"), &fx)

	for _, tc := range fx.Cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			httpHarness := newControlTransportParityHarness(t, tc.Scenario)
			controlHarness := newControlTransportParityHarness(t, tc.Scenario)

			httpResult, httpErr := httpHarness.callHTTP(t, tc.Method, tc.Params)
			controlResult, controlErr := controlHarness.callControl(t, tc.Method, tc.Params)

			if tc.ExpectErrorContains != "" {
				if httpErr == "" || !strings.Contains(httpErr, tc.ExpectErrorContains) {
					t.Fatalf("http error=%q want contains %q", httpErr, tc.ExpectErrorContains)
				}
				if controlErr == "" || !strings.Contains(controlErr, tc.ExpectErrorContains) {
					t.Fatalf("control error=%q want contains %q", controlErr, tc.ExpectErrorContains)
				}
				return
			}

			if httpErr != "" || controlErr != "" {
				t.Fatalf("unexpected transport mismatch http_err=%q control_err=%q", httpErr, controlErr)
			}

			gotHTTP := normalizeParityValue(t, httpResult, tc.IgnoreResultKeys, tc.IgnoreResultPaths)
			gotControl := normalizeParityValue(t, controlResult, tc.IgnoreResultKeys, tc.IgnoreResultPaths)
			if !reflect.DeepEqual(gotHTTP, gotControl) {
				httpJSON, _ := json.MarshalIndent(gotHTTP, "", "  ")
				controlJSON, _ := json.MarshalIndent(gotControl, "", "  ")
				t.Fatalf("transport parity drift for %s\nhttp=%s\ncontrol=%s", tc.Method, httpJSON, controlJSON)
			}
		})
	}
}

func newControlTransportParityHarness(t *testing.T, scenario string) *controlTransportParityHarness {
	t.Helper()

	cfg := controlTransportParityConfig(scenario)
	cfgState := newRuntimeConfigStore(cfg)
	store := newTestStore()
	docsRepo := state.NewDocsRepository(store, "parity-author")
	transcriptRepo := state.NewTranscriptRepository(store, "parity-author")
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	tools := agent.NewToolRegistry()
	dmBus := &parityDMTransport{pubkey: "parity-pubkey", relays: append([]string{}, cfg.Relays.Read...)}
	startedAt := time.Now().Add(-5 * time.Second)

	prevSessionStore := controlSessionStore
	prevServices := controlServices
	controlSessionStore = sessionStore
	controlServices = &daemonServices{
		emitterMu: &sync.RWMutex{},
		relay: relayPolicyServices{
			dmBusMu: &sync.RWMutex{},
			dmBus:   new(nostruntime.DMTransport),
		},
		session: sessionServices{
			toolRegistry:  tools,
			sessionStore:  sessionStore,
			sessionRouter: agent.NewAgentSessionRouter(),
			agentJobs:     newAgentJobRegistry(),
			subagents:     newSubagentRegistry(),
			ops:           newOperationsRegistry(),
		},
		runtimeConfig: cfgState,
	}
	t.Cleanup(func() {
		controlSessionStore = prevSessionStore
		controlServices = prevServices
	})

	seedControlTransportParityScenario(t, scenario, docsRepo, transcriptRepo, sessionStore)

	opts := admin.ServerOptions{
		Status: admin.StatusProvider{
			PubKey:   "parity-pubkey",
			Relays:   append([]string{}, cfg.Relays.Read...),
			DMPolicy: cfg.DM.Policy,
			Started:  startedAt,
		},
		StatusDMPolicy: func() string {
			return cfgState.Get().DM.Policy
		},
		StatusRelays: func() []string {
			current := cfgState.Get()
			return append([]string{}, current.Relays.Read...)
		},
		GetConfig: func(context.Context) (state.ConfigDoc, error) {
			return cfgState.Get(), nil
		},
		PutConfig: func(ctx context.Context, newCfg state.ConfigDoc) error {
			cfgState.Set(newCfg)
			if _, err := docsRepo.PutConfig(ctx, newCfg); err != nil {
				return err
			}
			return nil
		},
		ConfigSet: func(ctx context.Context, req methods.ConfigSetRequest) (map[string]any, int, error) {
			return dispatchAdminControlConfigMutation(ctx, "parity-caller", methods.MethodConfigSet, req, dmBus, nil, nil, nil, nil, nil, docsRepo, transcriptRepo, nil, cfgState, tools, nil, startedAt)
		},
		ConfigApply: func(ctx context.Context, req methods.ConfigApplyRequest) (map[string]any, int, error) {
			return dispatchAdminControlConfigMutation(ctx, "parity-caller", methods.MethodConfigApply, req, dmBus, nil, nil, nil, nil, nil, docsRepo, transcriptRepo, nil, cfgState, tools, nil, startedAt)
		},
		ConfigPatch: func(ctx context.Context, req methods.ConfigPatchRequest) (map[string]any, int, error) {
			return dispatchAdminControlConfigMutation(ctx, "parity-caller", methods.MethodConfigPatch, req, dmBus, nil, nil, nil, nil, nil, docsRepo, transcriptRepo, nil, cfgState, tools, nil, startedAt)
		},
		SupportedMethods: func(context.Context) ([]string, error) {
			return supportedMethods(cfgState.Get()), nil
		},
		DelegateControlCall: func(ctx context.Context, method string, params json.RawMessage) (any, int, error) {
			return dispatchAdminDelegatedControlCall(ctx, admin.CallerPubKeyFromContext(ctx), method, params, dmBus, nil, nil, nil, nil, nil, docsRepo, transcriptRepo, nil, cfgState, tools, nil, startedAt)
		},
		ListSessions: func(ctx context.Context, limit int) ([]state.SessionDoc, error) {
			return docsRepo.ListSessions(ctx, limit)
		},
		SessionStore: sessionStore,
		ListTranscript: func(ctx context.Context, sessionID string, limit int) ([]state.TranscriptEntryDoc, error) {
			return transcriptRepo.ListSession(ctx, sessionID, limit)
		},
		AgentIdentity: func(ctx context.Context, req methods.AgentIdentityRequest) (map[string]any, error) {
			agentID := strings.TrimSpace(req.AgentID)
			if agentID == "" {
				agentID = "main"
			}
			displayName := "Metiq Agent"
			if doc, err := docsRepo.GetAgent(ctx, agentID); err == nil && doc.Name != "" {
				displayName = doc.Name
			}
			return map[string]any{
				"agent_id":     agentID,
				"display_name": displayName,
				"session_id":   req.SessionID,
				"pubkey":       "parity-pubkey",
			}, nil
		},
		Send: func(ctx context.Context, req methods.SendRequest) (map[string]any, error) {
			result, _, err := dispatchAdminControlConfigMutation(ctx, "parity-caller", methods.MethodSend, req, dmBus, nil, nil, nil, nil, nil, docsRepo, transcriptRepo, nil, cfgState, tools, nil, startedAt)
			return result, err
		},
		ToolsCatalog: func(ctx context.Context, req methods.ToolsCatalogRequest) (map[string]any, error) {
			if err := isKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
				return nil, err
			}
			current := cfgState.Get()
			agentID := defaultAgentID(req.AgentID)
			groups := buildToolCatalogGroups(current, tools, req.IncludePlugins, nil)
			if req.Profile != nil && *req.Profile != "" {
				profileID := strings.TrimSpace(strings.ToLower(*req.Profile))
				if agent.LookupProfile(profileID) == nil {
					return nil, fmt.Errorf("unknown profile %q; valid: %s", profileID, strings.Join(agent.ProfileListSorted(), ", "))
				}
				groups = agent.FilterCatalogByProfile(groups, profileID)
			}
			return map[string]any{
				"agentId":  agentID,
				"profiles": defaultToolProfiles(),
				"groups":   groups,
			}, nil
		},
	}

	baseURL, cleanup := startControlTransportParityAdminServer(t, opts)
	t.Cleanup(cleanup)

	return &controlTransportParityHarness{
		baseURL:        baseURL,
		client:         &http.Client{Timeout: 2 * time.Second},
		dmBus:          dmBus,
		cfgState:       cfgState,
		docsRepo:       docsRepo,
		transcriptRepo: transcriptRepo,
		sessionStore:   sessionStore,
		tools:          tools,
		startedAt:      startedAt,
	}
}

func (h *controlTransportParityHarness) callHTTP(t *testing.T, method string, params map[string]any) (any, string) {
	t.Helper()

	raw, err := json.Marshal(map[string]any{"method": method, "params": params})
	if err != nil {
		t.Fatalf("marshal http request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, h.baseURL+"/call", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new http request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := h.client.Do(req)
	if err != nil {
		t.Fatalf("http call failed: %v", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read http response: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode http response: %v body=%s", err, string(body))
	}
	if ok, _ := decoded["ok"].(bool); ok {
		return decoded["result"], ""
	}
	if errMsg, _ := decoded["error"].(string); errMsg != "" {
		return nil, errMsg
	}
	t.Fatalf("unexpected http envelope status=%d body=%s", res.StatusCode, string(body))
	return nil, ""
}

func (h *controlTransportParityHarness) callControl(t *testing.T, method string, params map[string]any) (any, string) {
	t.Helper()

	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal control params: %v", err)
	}
	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "parity-caller",
		Method:     method,
		Params:     raw,
	}, h.dmBus, nil, nil, nil, nil, nil, h.docsRepo, h.transcriptRepo, nil, h.cfgState, h.tools, nil, h.startedAt)
	if err != nil {
		return nil, err.Error()
	}
	return res.Result, ""
}

func startControlTransportParityAdminServer(t *testing.T, opts admin.ServerOptions) (string, func()) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	opts.Addr = addr
	go func() {
		errCh <- admin.Start(ctx, opts)
	}()

	baseURL := "http://" + addr
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(3 * time.Second)
	for {
		req, err := http.NewRequest(http.MethodGet, baseURL+"/healthz", nil)
		if err != nil {
			t.Fatalf("new health request: %v", err)
		}
		res, err := client.Do(req)
		if err == nil {
			res.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			select {
			case startErr := <-errCh:
				t.Fatalf("admin server did not start: %v", startErr)
			default:
				t.Fatalf("admin server did not start before deadline: %v", err)
			}
		}
		time.Sleep(25 * time.Millisecond)
	}

	cleanup := func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("admin shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("admin shutdown timed out")
		}
	}
	return baseURL, cleanup
}

func controlTransportParityConfig(scenario string) state.ConfigDoc {
	cfg := state.ConfigDoc{
		Version: 1,
		Control: state.ControlPolicy{RequireAuth: false},
		DM:      state.DMPolicy{Policy: "open"},
		Relays: state.RelayPolicy{
			Read:  []string{"wss://relay.example.com"},
			Write: []string{"wss://relay.example.com"},
		},
	}
	if scenario == "pairing_config" {
		cfg.DM.Policy = "pairing"
	}
	return cfg
}

func seedControlTransportParityScenario(t *testing.T, scenario string, docsRepo *state.DocsRepository, transcriptRepo *state.TranscriptRepository, sessionStore *state.SessionStore) {
	t.Helper()

	if scenario != "seeded_sessions" {
		return
	}

	ctx := context.Background()
	if _, err := docsRepo.PutSession(ctx, "session-1", state.SessionDoc{
		Version:       1,
		SessionID:     "session-1",
		PeerPubKey:    "peer-a",
		LastInboundAt: time.Now().Add(-2 * time.Minute).Unix(),
		Meta: map[string]any{
			"label":    "Briefing",
			"agent_id": "main",
		},
	}); err != nil {
		t.Fatalf("seed session doc: %v", err)
	}
	entry := sessionStore.GetOrNew("session-1")
	entry.SessionID = "session-1"
	entry.AgentID = "main"
	entry.Label = "Briefing"
	entry.LastTo = "peer-a"
	entry.InputTokens = 12
	entry.OutputTokens = 34
	if err := sessionStore.Put("session-1", entry); err != nil {
		t.Fatalf("seed session store: %v", err)
	}
	if _, err := transcriptRepo.PutEntry(ctx, state.TranscriptEntryDoc{
		Version:   1,
		SessionID: "session-1",
		EntryID:   "entry-1",
		Role:      "user",
		Text:      "Plan the briefing memo",
		Unix:      time.Now().Add(-90 * time.Second).Unix(),
	}); err != nil {
		t.Fatalf("seed transcript user entry: %v", err)
	}
	if _, err := transcriptRepo.PutEntry(ctx, state.TranscriptEntryDoc{
		Version:   1,
		SessionID: "session-1",
		EntryID:   "entry-2",
		Role:      "assistant",
		Text:      "Drafting the briefing memo now.",
		Unix:      time.Now().Add(-30 * time.Second).Unix(),
	}); err != nil {
		t.Fatalf("seed transcript assistant entry: %v", err)
	}
}

func normalizeParityValue(t *testing.T, value any, ignoreKeys []string, ignorePaths []string) any {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal parity value: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode parity value: %v", err)
	}
	root, ok := decoded.(map[string]any)
	if ok {
		for _, key := range ignoreKeys {
			delete(root, key)
		}
	}
	for _, path := range ignorePaths {
		stripIgnoredParityPath(decoded, splitIgnoredParityPath(path))
	}
	return decoded
}

func splitIgnoredParityPath(path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	segments := make([]string, 0, 4)
	var current strings.Builder
	flush := func() {
		if current.Len() == 0 {
			return
		}
		segments = append(segments, current.String())
		current.Reset()
	}
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '.':
			flush()
		case '[':
			flush()
			if strings.HasPrefix(path[i:], "[*]") {
				segments = append(segments, "*")
				i += 2
			}
		default:
			current.WriteByte(path[i])
		}
	}
	flush()
	return segments
}

func stripIgnoredParityPath(node any, segments []string) {
	if len(segments) == 0 {
		return
	}
	switch typed := node.(type) {
	case map[string]any:
		segment := segments[0]
		if segment == "*" {
			return
		}
		if len(segments) == 1 {
			delete(typed, segment)
			return
		}
		next, ok := typed[segment]
		if !ok {
			return
		}
		stripIgnoredParityPath(next, segments[1:])
	case []any:
		if segments[0] != "*" {
			return
		}
		for _, item := range typed {
			stripIgnoredParityPath(item, segments[1:])
		}
	}
}
