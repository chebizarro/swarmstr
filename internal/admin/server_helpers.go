package admin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

type callerPubKeyKey string

const callerPubKeyContextKey callerPubKeyKey = "admin-caller-pubkey"

func CallerPubKeyFromContext(ctx context.Context) string {
	caller, _ := ctx.Value(callerPubKeyContextKey).(string)
	return strings.TrimSpace(caller)
}

func delegateControlCall(ctx context.Context, opts ServerOptions, method string, params json.RawMessage, notConfigured string) (any, int, error) {
	if opts.DelegateControlCall == nil {
		return nil, http.StatusNotImplemented, errors.New(notConfigured)
	}
	return opts.DelegateControlCall(ctx, method, params)
}

func canonicalMethodName(method string) string {
	switch strings.TrimSpace(method) {
	case methods.MethodStatusAlias:
		return methods.MethodStatus
	default:
		return method
	}
}

func isNIP86RPC(r *http.Request) bool {
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	accept := strings.ToLower(strings.TrimSpace(r.Header.Get("Accept")))
	if strings.Contains(ct, "application/nostr+json+rpc") || strings.Contains(accept, "application/nostr+json+rpc") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("profile")), "nip86")
}

func parseLimit(raw string, def, max int) int {
	limit := def
	if strings.TrimSpace(raw) != "" {
		fmt.Sscanf(raw, "%d", &limit)
	}
	if limit <= 0 {
		limit = def
	}
	if limit > max {
		limit = max
	}
	return limit
}

func handleStateError(w http.ResponseWriter, err error) {
	if errors.Is(err, state.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
}

func withAuth(token string, next http.HandlerFunc) http.HandlerFunc {
	if strings.TrimSpace(token) == "" {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		parts := strings.Fields(auth)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") || subtle.ConstantTimeCompare([]byte(parts[1]), []byte(token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		ctx := context.WithValue(r.Context(), tokenAuthContextKey, true)
		next(w, r.WithContext(ctx))
	}
}

func validateExposure(addr string, token string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid admin addr %q: %w", addr, err)
	}
	if strings.TrimSpace(token) != "" {
		return nil
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("admin token required for non-loopback bind address")
}

func writeNIP86JSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/nostr+json+rpc")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}

func internalRoutingError(domain, method string) (any, int, error) {
	return nil, http.StatusInternalServerError, fmt.Errorf("internal routing bug: method %q reached %s dispatcher", method, domain)
}

func mergeSessionMeta(base map[string]any, patch map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range patch {
		if v == nil {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	return out
}
