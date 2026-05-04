package policy

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/store/state"
)

type ControlAuth struct {
	Authenticated bool
	CallerPubKey  string
	Reason        string
}

type ControlDecision struct {
	Allowed       bool
	Authenticated bool
	Reason        string
}

func AuthenticateControlCall(r *http.Request, payload []byte, maxAge time.Duration) ControlAuth {
	authHeader := strings.TrimSpace(r.Header.Get("X-Nostr-Authorization"))
	if authHeader == "" {
		authHeader = strings.TrimSpace(r.Header.Get("Authorization"))
	}
	if authHeader == "" {
		return ControlAuth{Reason: "missing nostr authorization"}
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 {
		return ControlAuth{Reason: "invalid nostr authorization scheme"}
	}
	if !strings.EqualFold(parts[0], "nostr") {
		if strings.EqualFold(parts[0], "bearer") {
			return ControlAuth{Reason: "nostr authorization must be provided in X-Nostr-Authorization when bearer auth is used"}
		}
		return ControlAuth{Reason: "invalid nostr authorization scheme"}
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
	if err != nil {
		return ControlAuth{Reason: "invalid nostr authorization encoding"}
	}
	var evt nostr.Event
	if err := json.Unmarshal(decoded, &evt); err != nil {
		return ControlAuth{Reason: "invalid nostr authorization event"}
	}
	if !evt.VerifySignature() {
		return ControlAuth{Reason: "invalid nostr authorization signature"}
	}
	if evt.Kind != nostr.Kind(27235) {
		return ControlAuth{Reason: "invalid nostr authorization event kind"}
	}
	methodTag := evt.Tags.Find("method")
	if methodTag == nil || len(methodTag) < 2 || !strings.EqualFold(strings.TrimSpace(methodTag[1]), strings.TrimSpace(r.Method)) {
		return ControlAuth{Reason: "invalid method tag in nostr authorization"}
	}

	uTag := evt.Tags.Find("u")
	if uTag == nil || len(uTag) < 2 {
		return ControlAuth{Reason: "missing u tag in nostr authorization"}
	}
	expectedURL := requestURL(r)
	if nostr.NormalizeURL(uTag[1]) != nostr.NormalizeURL(expectedURL) {
		return ControlAuth{Reason: "invalid u tag in nostr authorization"}
	}

	hash := sha256.Sum256(payload)
	expectedPayload := nostr.HexEncodeToString(hash[:])
	if evt.Tags.FindWithValue("payload", expectedPayload) == nil {
		return ControlAuth{Reason: "invalid payload hash in nostr authorization"}
	}

	now := nostr.Now()
	maxAgeSeconds := int64(maxAge.Seconds())
	if maxAgeSeconds <= 0 {
		maxAgeSeconds = 30
	}
	if evt.CreatedAt < now-nostr.Timestamp(maxAgeSeconds) {
		return ControlAuth{Reason: "nostr authorization event too old"}
	}
	if evt.CreatedAt > now+nostr.Timestamp(30) {
		return ControlAuth{Reason: "nostr authorization event from the future"}
	}

	return ControlAuth{Authenticated: true, CallerPubKey: strings.ToLower(evt.PubKey.Hex())}
}

func EvaluateControlCall(callerPubKey, method string, authenticated bool, cfg state.ConfigDoc) ControlDecision {
	method = normalizeControlMethod(method)
	if method == "" {
		return ControlDecision{Allowed: false, Authenticated: authenticated, Reason: "method is required"}
	}
	policy := cfg.Control
	sensitive := IsSensitiveControlMethod(method)
	if !authenticated {
		if sensitive {
			return ControlDecision{Allowed: false, Authenticated: false, Reason: "authenticated control admin required"}
		}
		if methodAllowedForUnauth(method, policy.AllowUnauthMethods) || !policy.RequireAuth {
			return ControlDecision{Allowed: true, Authenticated: false}
		}
		return ControlDecision{Allowed: false, Authenticated: false, Reason: "authentication required"}
	}

	normCaller := normalizePubKey(callerPubKey)
	if len(policy.Admins) == 0 {
		if policy.RequireAuth || sensitive {
			return ControlDecision{Allowed: false, Authenticated: true, Reason: "no control admins configured"}
		}
		return ControlDecision{Allowed: true, Authenticated: true}
	}

	for _, admin := range policy.Admins {
		if normalizePubKey(admin.PubKey) != normCaller {
			continue
		}
		if len(admin.Methods) == 0 || methodAllowed(method, admin.Methods) {
			return ControlDecision{Allowed: true, Authenticated: true}
		}
		return ControlDecision{Allowed: false, Authenticated: true, Reason: fmt.Sprintf("method %q not allowed for caller", method)}
	}
	return ControlDecision{Allowed: false, Authenticated: true, Reason: "caller is not an admin"}
}

func IsSensitiveControlMethod(method string) bool {
	switch normalizeControlMethod(method) {
	case "config.put", "config.set", "config.apply", "config.patch", "list.put":
		return true
	case "agents.create", "agents.update", "agents.delete", "agents.assign", "agents.unassign", "agents.files.set":
		return true
	case "tools.profile.set", "skills.install", "skills.update":
		return true
	case "cron.add", "cron.update", "cron.remove", "cron.run":
		return true
	case "exec.approvals.set", "exec.approval.resolve":
		return true
	case "mcp.put", "mcp.remove", "mcp.reconnect", "mcp.auth.start", "mcp.auth.refresh", "mcp.auth.clear", "secrets.reload", "secrets.resolve":
		return true
	case "talk.mode", "voicewake.set", "tts.setprovider", "tts.enable", "tts.disable", "tts.convert", "browser.request":
		return true
	case "node.pair.approve", "node.pair.reject", "device.pair.approve", "device.pair.reject", "device.pair.remove", "device.token.rotate", "device.token.revoke", "node.rename", "node.canvas.capability.refresh", "node.invoke", "node.event", "node.result", "node.invoke.result", "node.pending.enqueue", "node.pending.pull", "node.pending.ack", "node.pending.drain", "exec.approvals.node.set", "canvas.update", "canvas.delete":
		return true
	case "plugins.install", "plugins.uninstall", "plugins.update":
		return true
	case "channels.logout", "channels.join", "channels.leave", "channels.send":
		return true
	case "chat.send", "sessions.patch", "sessions.reset", "sessions.delete", "sessions.compact", "sessions.prune", "sessions.spawn", "chat.abort", "memory.compact":
		return true
	case "tasks.create", "tasks.cancel", "tasks.resume", "tasks.audit_export":
		return true
	case "acp.register", "acp.unregister", "acp.dispatch", "acp.pipeline":
		return true
	case "sandbox.run", "wizard.start", "wizard.next", "wizard.cancel", "update.run", "set-heartbeats", "send", "hooks.enable", "hooks.disable":
		return true
	default:
		return false
	}
}

func IsUnauthAllowedControlMethod(method string) bool {
	switch normalizeControlMethod(method) {
	case "supportedmethods", "health", "status.get", "status":
		return true
	default:
		return false
	}
}

func requestURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if xf := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); xf != "" {
		scheme = strings.ToLower(xf)
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = strings.TrimSpace(r.URL.Host)
	}
	path := r.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	if strings.TrimSpace(r.URL.RawQuery) != "" {
		return fmt.Sprintf("%s://%s%s?%s", scheme, host, path, r.URL.RawQuery)
	}
	return fmt.Sprintf("%s://%s%s", scheme, host, path)
}

func methodAllowed(method string, allowed []string) bool {
	method = normalizeControlMethod(method)
	if method == "" {
		return false
	}
	for _, entry := range allowed {
		rule := normalizeControlMethod(entry)
		if rule == "" {
			continue
		}
		if rule == "*" || rule == method {
			return true
		}
		if strings.HasSuffix(rule, ".*") {
			prefix := strings.TrimSuffix(rule, "*")
			if strings.HasPrefix(method, prefix) {
				return true
			}
		}
	}
	return false
}

func methodAllowedForUnauth(method string, allowed []string) bool {
	method = normalizeControlMethod(method)
	if method == "" || IsSensitiveControlMethod(method) {
		return false
	}
	for _, entry := range allowed {
		rule := normalizeControlMethod(entry)
		if !IsUnauthAllowedControlMethod(rule) {
			continue
		}
		if rule == method {
			return true
		}
	}
	return false
}

func normalizeControlMethod(method string) string {
	return strings.ToLower(strings.TrimSpace(method))
}
