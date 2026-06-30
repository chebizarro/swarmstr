package acp

import "strings"

// RouteMode identifies how an ACP harness request should be executed.
type RouteMode string

const (
	// RouteModeRuntime routes through the managed OpenClaw ACP runtime path
	// (for example sessions_spawn with runtime:"acp").
	RouteModeRuntime RouteMode = "runtime"
	// RouteModeDirect routes through direct acpx CLI invocation.
	RouteModeDirect RouteMode = "direct"
)

// RouteIntent describes the caller's high-level ACP routing intent.
type RouteIntent string

const (
	// RouteIntentSession is the default persistent session flow.
	RouteIntentSession RouteIntent = "session"
	// RouteIntentOneShot is a single prompt/exec flow.
	RouteIntentOneShot RouteIntent = "oneshot"
	// RouteIntentThreadSpawn is sessions_spawn(runtime:"acp") style thread creation.
	RouteIntentThreadSpawn RouteIntent = "thread_spawn"
	// RouteIntentRelay is a telephone-game style relay to a harness.
	RouteIntentRelay RouteIntent = "relay"
)

// RetryPolicy describes caller-visible repair/retry behavior for a route.
type RetryPolicy struct {
	// RepairBeforeRetry tells the caller to attempt local acpx/runtime repair before retrying.
	RepairBeforeRetry bool
	// RestartGatewayAfterRepair tells the caller that a successful repair requires gateway restart.
	RestartGatewayAfterRepair bool
	// MaxRetries is the number of automatic retries after any repair attempt.
	MaxRetries int
	// FallbackModes are acceptable fallbacks, ordered by preference, if retry fails.
	FallbackModes []RouteMode
}

// Route describes policy for a harness alias or canonical harness name.
type Route struct {
	// AgentID is the canonical acpx/ACP harness id.
	AgentID string
	// Mode is the preferred execution mode for this harness.
	Mode RouteMode
	// Backend is the ACP runtime backend id. Empty means use the manager default.
	Backend string
	// SessionMode is the ACP manager lifecycle mode to use for runtime sessions.
	SessionMode SessionMode
	// Retry is the default retry/repair policy for this harness.
	Retry RetryPolicy
}

// RouteRequest is the pure-policy input for Router.Route.
type RouteRequest struct {
	// Harness is a user-facing alias or canonical harness name, such as "claude code".
	Harness string
	// AgentID is an explicit canonical harness id. If set it takes precedence over Harness aliases.
	AgentID string
	// Intent influences runtime/direct selection.
	Intent RouteIntent
	// Runtime explicitly requests runtime routing when true.
	Runtime bool
	// Direct explicitly requests direct acpx routing when true.
	Direct bool
	// RuntimeUnavailable selects the direct fallback policy when the ACP runtime is unavailable.
	RuntimeUnavailable bool
	// OneShot requests oneshot lifecycle/exec behavior.
	OneShot bool
}

// RouteDecision is the immutable routing decision produced by Router.Route.
type RouteDecision struct {
	Mode          RouteMode
	AgentID       string
	TargetHarness string
	Backend       string
	SessionMode   SessionMode
	Thread        bool
	Retry         RetryPolicy
}

// Router is a self-contained ACP harness routing policy table.
type Router struct {
	defaultAgentID string
	routes         map[string]Route
	aliases        map[string]string
}

// NewRouter returns a Router with sensible default ACP harness aliases and policy.
func NewRouter() *Router {
	r := &Router{
		defaultAgentID: "openclaw",
		routes:         make(map[string]Route),
		aliases:        make(map[string]string),
	}
	for _, id := range []string{"openclaw", "claude", "codex", "copilot", "cursor", "droid", "opencode", "gemini", "iflow", "kilocode", "kimi", "kiro", "qwen"} {
		r.SetRoute(id, Route{AgentID: id, Mode: RouteModeRuntime, SessionMode: SessionModePersistent, Retry: defaultRetryPolicy()})
	}
	for alias, id := range map[string]string{
		"claude code":    "claude",
		"github copilot": "copilot",
		"cursor cli":     "cursor",
		"factory droid":  "droid",
		"gemini cli":     "gemini",
		"kimi cli":       "kimi",
		"kiro cli":       "kiro",
		"qwen code":      "qwen",
	} {
		r.SetAlias(alias, id)
	}
	return r
}

// SetDefaultAgent sets the fallback harness used when a request has no harness.
func (r *Router) SetDefaultAgent(agentID string) {
	if r == nil {
		return
	}
	if id := normalizeRouteKey(agentID); id != "" {
		r.defaultAgentID = id
	}
}

// SetAlias maps a user-facing alias to a canonical harness id.
func (r *Router) SetAlias(alias, agentID string) {
	if r == nil {
		return
	}
	alias = normalizeRouteKey(alias)
	agentID = normalizeRouteKey(agentID)
	if alias == "" || agentID == "" {
		return
	}
	r.aliases[alias] = agentID
}

// SetRoute adds or replaces policy for a canonical harness id.
func (r *Router) SetRoute(agentID string, route Route) {
	if r == nil {
		return
	}
	agentID = normalizeRouteKey(firstNonEmpty(route.AgentID, agentID))
	if agentID == "" {
		return
	}
	if route.AgentID == "" {
		route.AgentID = agentID
	} else {
		route.AgentID = normalizeRouteKey(route.AgentID)
	}
	if route.Mode == "" {
		route.Mode = RouteModeRuntime
	}
	if route.SessionMode == "" {
		route.SessionMode = SessionModePersistent
	}
	if route.Retry.MaxRetries == 0 && !route.Retry.RepairBeforeRetry && len(route.Retry.FallbackModes) == 0 {
		route.Retry = defaultRetryPolicy()
	}
	r.routes[agentID] = cloneRoute(route)
}

// Route returns the ACP harness routing decision for req.
func (r *Router) Route(req RouteRequest) RouteDecision {
	if r == nil {
		r = NewRouter()
	}
	agentID := r.resolveAgentID(req)
	route, ok := r.routes[agentID]
	if !ok {
		route = Route{AgentID: agentID, Mode: RouteModeRuntime, SessionMode: SessionModePersistent, Retry: defaultRetryPolicy()}
	}
	mode := route.Mode
	if req.Direct || req.Intent == RouteIntentRelay || req.RuntimeUnavailable {
		mode = RouteModeDirect
	}
	if req.Runtime || req.Intent == RouteIntentThreadSpawn {
		mode = RouteModeRuntime
	}
	sessionMode := route.SessionMode
	if req.OneShot || req.Intent == RouteIntentOneShot {
		sessionMode = SessionModeOneshot
	}
	decision := RouteDecision{
		Mode:          mode,
		AgentID:       route.AgentID,
		TargetHarness: route.AgentID,
		Backend:       normalizeRouteKey(route.Backend),
		SessionMode:   sessionMode,
		Thread:        req.Intent == RouteIntentThreadSpawn,
		Retry:         cloneRetryPolicy(route.Retry),
	}
	if decision.Mode == RouteModeDirect && req.RuntimeUnavailable {
		decision.Retry = runtimeUnavailableRetryPolicy()
	}
	if decision.Thread {
		decision.Retry = threadSpawnRetryPolicy()
	}
	return decision
}

func (r *Router) resolveAgentID(req RouteRequest) string {
	if id := normalizeRouteKey(req.AgentID); id != "" {
		return id
	}
	if h := normalizeRouteKey(req.Harness); h != "" {
		if id := r.aliases[h]; id != "" {
			return id
		}
		return h
	}
	return r.defaultAgentID
}

func defaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxRetries: 0}
}

func runtimeUnavailableRetryPolicy() RetryPolicy {
	return RetryPolicy{
		RepairBeforeRetry:         true,
		RestartGatewayAfterRepair: true,
		MaxRetries:                1,
		FallbackModes:             []RouteMode{RouteModeRuntime, RouteModeDirect},
	}
}

func threadSpawnRetryPolicy() RetryPolicy {
	return RetryPolicy{
		RepairBeforeRetry:         true,
		RestartGatewayAfterRepair: true,
		MaxRetries:                1,
		FallbackModes:             []RouteMode{RouteModeRuntime, RouteModeDirect},
	}
}

func cloneRoute(in Route) Route {
	out := in
	out.Retry = cloneRetryPolicy(in.Retry)
	return out
}

func cloneRetryPolicy(in RetryPolicy) RetryPolicy {
	out := in
	out.FallbackModes = append([]RouteMode(nil), in.FallbackModes...)
	return out
}

func normalizeRouteKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
