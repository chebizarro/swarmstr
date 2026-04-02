package admin

// webhook_ingress.go — HTTP ingress for external webhook triggers.
//
// When hooks.enabled=true in the runtime ConfigDoc, the admin server exposes:
//
//	POST /hooks/wake   — enqueue a system event (fire-and-forget)
//	POST /hooks/agent  — run an isolated agent turn (fire-and-forget)
//	POST /hooks/<name> — matched against hooks.mappings[].match.path
//
// Auth: "Authorization: Bearer <token>" or "X-Metiq-Token: <token>".
// Query-string tokens are not accepted.
// Repeated auth failures are rate-limited per remote IP.

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

// ─── Rate limiter ─────────────────────────────────────────────────────────────

const (
	hooksAuthFailMax     = 10
	hooksAuthBanDuration = 60 * time.Second
)

type hooksBanEntry struct {
	count   int
	resetAt time.Time
}

var (
	hooksBanMu  sync.Mutex
	hooksBanMap = map[string]*hooksBanEntry{}
)

func hooksCheckRateLimit(ip string) bool {
	hooksBanMu.Lock()
	defer hooksBanMu.Unlock()
	e, ok := hooksBanMap[ip]
	if !ok {
		return true
	}
	if time.Now().After(e.resetAt) {
		delete(hooksBanMap, ip)
		return true
	}
	return e.count < hooksAuthFailMax
}

func hooksRecordAuthFailure(ip string) {
	hooksBanMu.Lock()
	defer hooksBanMu.Unlock()
	e, ok := hooksBanMap[ip]
	if !ok || time.Now().After(e.resetAt) {
		hooksBanMap[ip] = &hooksBanEntry{count: 1, resetAt: time.Now().Add(hooksAuthBanDuration)}
		return
	}
	e.count++
}

// ─── Auth ─────────────────────────────────────────────────────────────────────

func hooksExtractToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if strings.HasPrefix(auth, "Bearer ") {
			return strings.TrimPrefix(auth, "Bearer ")
		}
	}
	if t := r.Header.Get("X-Metiq-Token"); t != "" {
		return t
	}
	return ""
}

// ─── Request payload types ────────────────────────────────────────────────────

type hooksWakePayload struct {
	Text string `json:"text"`
	Mode string `json:"mode,omitempty"`
}

type hooksAgentPayload struct {
	Message        string `json:"message"`
	Name           string `json:"name,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	SessionKey     string `json:"session_key,omitempty"`
	WakeMode       string `json:"wake_mode,omitempty"`
	Deliver        *bool  `json:"deliver,omitempty"`
	Channel        string `json:"channel,omitempty"`
	To             string `json:"to,omitempty"`
	Model          string `json:"model,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// ─── Template interpolation ───────────────────────────────────────────────────

// interpolateTemplate replaces {{key.path}} tokens in tmpl with values looked
// up in body using dot-separated key paths.
//
//	tmpl:  "event {{action}} on {{repository.full_name}}"
//	body:  {"action":"opened","repository":{"full_name":"org/repo"}}
//	→      "event opened on org/repo"
func interpolateTemplate(tmpl string, body map[string]any) string {
	result := tmpl
	for {
		start := strings.Index(result, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(result[start:], "}}")
		if end < 0 {
			break
		}
		end += start
		key := result[start+2 : end]
		val := lookupDotPath(body, key)
		result = result[:start] + fmt.Sprintf("%v", val) + result[end+2:]
	}
	return result
}

func lookupDotPath(m map[string]any, path string) any {
	head, tail, hasTail := strings.Cut(path, ".")
	v, ok := m[head]
	if !ok {
		return ""
	}
	if !hasTail {
		return v
	}
	sub, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	return lookupDotPath(sub, tail)
}

// ─── Mount ────────────────────────────────────────────────────────────────────

// mountWebhookIngress registers the /hooks/ prefix handler on mux.
// The ingress only activates when hooks.enabled=true in the runtime ConfigDoc.
// opts.GetConfig is called on every request to pick up live config changes.
func mountWebhookIngress(mux *http.ServeMux, opts ServerOptions) {
	if opts.GetConfig == nil {
		return
	}
	mux.HandleFunc("/hooks/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}

		// Read runtime config; treat unavailability as "not found".
		cfg, err := opts.GetConfig(r.Context())
		if err != nil || !cfg.Hooks.Enabled {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		hcfg := cfg.Hooks

		// Rate-limit check on remote IP.
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if !hooksCheckRateLimit(ip) {
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many auth failures"})
			return
		}

		// Require token in Authorization or X-Metiq-Token header.
		// Query-string tokens are intentionally rejected.
		tok := hooksExtractToken(r)
		if tok == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(hcfg.Token)) != 1 {
			hooksRecordAuthFailure(ip)
			w.Header().Set("WWW-Authenticate", `Bearer realm="metiq hooks"`)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}

		// Limit body size.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB

		// Dispatch on sub-path.
		sub := strings.TrimPrefix(r.URL.Path, "/hooks/")
		sub = strings.Trim(sub, "/")

		switch sub {
		case "wake":
			handleHooksWake(w, r, opts)
		case "agent":
			handleHooksAgent(w, r, opts, hcfg)
		default:
			handleHooksMapped(w, r, opts, hcfg, sub)
		}
	})
}

// ─── /hooks/wake ─────────────────────────────────────────────────────────────

func handleHooksWake(w http.ResponseWriter, r *http.Request, opts ServerOptions) {
	var p hooksWakePayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	p.Text = strings.TrimSpace(p.Text)
	if p.Text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "text is required"})
		return
	}

	// Respond immediately; run wake in the background.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "accepted"})

	if opts.Wake == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		req := methods.WakeRequest{
			Source: "webhook",
			Text:   p.Text,
			Mode:   p.Mode,
		}
		if _, err := opts.Wake(ctx, req); err != nil {
			log.Printf("webhook /hooks/wake: %v", err)
		}
	}()
}

// ─── /hooks/agent ────────────────────────────────────────────────────────────

func handleHooksAgent(w http.ResponseWriter, r *http.Request, opts ServerOptions, hcfg state.HooksConfig) {
	var p hooksAgentPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	p.Message = strings.TrimSpace(p.Message)
	if p.Message == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "message is required"})
		return
	}

	// Validate agent_id against allowlist when configured.
	if p.AgentID != "" && len(hcfg.AllowedAgentIDs) > 0 {
		allowed := false
		for _, id := range hcfg.AllowedAgentIDs {
			if id == "*" || id == p.AgentID {
				allowed = true
				break
			}
		}
		if !allowed {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "agent_id not in allowed_agent_ids"})
			return
		}
	}

	// Compute session key.
	sessionKey := hcfg.DefaultSessionKey
	if sessionKey == "" {
		sessionKey = "hook:ingress"
	}
	if p.AgentID != "" {
		sessionKey = "hook:" + p.AgentID
	}
	if sk := strings.TrimSpace(p.SessionKey); sk != "" {
		if !hcfg.AllowRequestSessionKey {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "session_key is not allowed"})
			return
		}
		sessionKey = sk
	}

	// deliver defaults to true when omitted.
	deliver := p.Deliver == nil || *p.Deliver

	// Respond immediately; run agent turn in the background.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "accepted"})

	if opts.StartAgent == nil {
		return
	}
	go func() {
		timeout := 120 * time.Second
		if p.TimeoutSeconds > 0 {
			timeout = time.Duration(p.TimeoutSeconds) * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		req := methods.AgentRequest{
			SessionID: sessionKey,
			Message:   p.Message,
		}
		if p.TimeoutSeconds > 0 {
			req.TimeoutMS = p.TimeoutSeconds * 1000
		}
		result, err := opts.StartAgent(ctx, req)
		if err != nil {
			log.Printf("webhook /hooks/agent: %v", err)
			return
		}

		// Deliver via Nostr DM when requested.
		if !deliver || opts.SendDM == nil {
			return
		}
		to := strings.TrimSpace(p.To)
		if to == "" {
			return
		}
		if p.Channel != "" && p.Channel != "nostr" {
			log.Printf("webhook /hooks/agent: delivery channel %q not supported (only nostr)", p.Channel)
			return
		}
		text := hooksExtractReplyText(result)
		if text == "" {
			return
		}
		if err := opts.SendDM(ctx, to, text); err != nil {
			log.Printf("webhook /hooks/agent delivery: %v", err)
		}
	}()
}

// ─── /hooks/<name> (custom mappings) ─────────────────────────────────────────

func handleHooksMapped(w http.ResponseWriter, r *http.Request, opts ServerOptions, hcfg state.HooksConfig, sub string) {
	// Find the first matching mapping.
	var mapping *state.HookMapping
	for i := range hcfg.Mappings {
		if hcfg.Mappings[i].Match.Path == sub {
			mapping = &hcfg.Mappings[i]
			break
		}
	}
	if mapping == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no mapping for " + sub})
		return
	}

	// Parse body as a generic JSON map for template interpolation.
	bodyBytes, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(bodyBytes, &body)
	if body == nil {
		body = map[string]any{}
	}

	msgText := mapping.MessageTemplate
	if msgText == "" {
		msgText = "Webhook event received: " + sub
	} else {
		msgText = interpolateTemplate(msgText, body)
	}

	switch mapping.Action {
	case "wake":
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "accepted"})
		if opts.Wake == nil {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := opts.Wake(ctx, methods.WakeRequest{
				Source: "webhook:" + sub,
				Text:   msgText,
			}); err != nil {
				log.Printf("webhook mapped [%s] wake: %v", sub, err)
			}
		}()

	case "agent":
		sessionKey := mapping.SessionKey
		if sessionKey == "" {
			sessionKey = "hook:" + sub
		}
		// Interpolate session_key template if needed.
		sessionKey = interpolateTemplate(sessionKey, body)

		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "accepted"})
		if opts.StartAgent == nil {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			result, err := opts.StartAgent(ctx, methods.AgentRequest{
				SessionID: sessionKey,
				Message:   msgText,
			})
			if err != nil {
				log.Printf("webhook mapped [%s] agent: %v", sub, err)
				return
			}
			if !mapping.Deliver || opts.SendDM == nil || strings.TrimSpace(mapping.To) == "" {
				return
			}
			if mapping.Channel != "" && mapping.Channel != "nostr" {
				return
			}
			text := hooksExtractReplyText(result)
			if text == "" {
				return
			}
			if err := opts.SendDM(ctx, mapping.To, text); err != nil {
				log.Printf("webhook mapped [%s] delivery: %v", sub, err)
			}
		}()

	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown action: " + mapping.Action})
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// hooksExtractReplyText pulls the agent reply text from the StartAgent result map.
// It checks common field names used by the agent runtime.
func hooksExtractReplyText(result map[string]any) string {
	for _, key := range []string{"text", "reply", "content", "message"} {
		if v, ok := result[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
