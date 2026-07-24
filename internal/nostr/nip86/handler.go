package nip86

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/nostr/nip98"
)

const ContentType = "application/nostr+json+rpc"

var SupportedMethods = []string{
	"banpubkey", "unbanpubkey", "listbannedpubkeys",
	"allowpubkey", "unallowpubkey", "listallowedpubkeys",
	"createrole", "editrole", "deleterole", "assignrole", "unassignrole",
	"listeventsneedingmoderation", "allowevent", "banevent", "listbannedevents",
	"changerelayname", "changerelaydescription", "changerelayicon",
	"allowkind", "disallowkind", "listallowedkinds",
	"blockip", "unblockip", "listblockedips",
}

type AdminPolicy func(context.Context, string) bool

type Handler struct {
	Store     ManagementStore
	RelayURL  string
	Authorize AdminPolicy
}
type request struct {
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}
type response struct {
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func NewHandler(store ManagementStore, relayURL string) http.Handler {
	if store == nil {
		store = NewMemoryStore()
	}
	var policy AdminPolicy
	if admins, ok := store.(interface {
		IsAdmin(context.Context, string) bool
	}); ok {
		policy = admins.IsAdmin
	}
	return NewHandlerWithAdminPolicy(store, relayURL, policy)
}

// NewHandlerWithAdminPolicy constructs a handler with an explicit policy. A nil
// policy fails closed after NIP-98 verification.
func NewHandlerWithAdminPolicy(store ManagementStore, relayURL string, policy AdminPolicy) http.Handler {
	if store == nil {
		store = NewMemoryStore()
	}
	return &Handler{Store: store, RelayURL: strings.TrimSpace(relayURL), Authorize: policy}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		write(w, response{Error: "method not allowed"})
		return
	}
	if !strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), ContentType) {
		write(w, response{Error: "unsupported content type"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		write(w, response{Error: "invalid request body"})
		return
	}
	u := h.RelayURL
	if u == "" {
		u = absoluteURL(r)
	}
	verifiedPubKey, err := nip98.VerifyPayloadRequired(r.Header.Get("Authorization"), r.Method, u, raw)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if h.Authorize == nil || !h.Authorize(r.Context(), verifiedPubKey) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	var req request
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		write(w, response{Error: "invalid request body"})
		return
	}
	res, err := h.Dispatch(r.Context(), strings.TrimSpace(req.Method), req.Params)
	if err != nil {
		write(w, response{Error: err.Error()})
		return
	}
	write(w, response{Result: res})
}

func (h *Handler) Dispatch(ctx context.Context, method string, params []json.RawMessage) (any, error) {
	if h.Store == nil {
		h.Store = NewMemoryStore()
	}
	switch method {
	case "supportedmethods":
		return append([]string(nil), SupportedMethods...), nil
	case "banpubkey":
		v, r, e := pubkeyReason(params)
		if e != nil {
			return nil, e
		}
		return true, h.Store.BanPubKey(ctx, v, r)
	case "unbanpubkey":
		v, r, e := pubkeyReason(params)
		if e != nil {
			return nil, e
		}
		return true, h.Store.UnbanPubKey(ctx, v, r)
	case "allowpubkey":
		v, r, e := pubkeyReason(params)
		if e != nil {
			return nil, e
		}
		return true, h.Store.AllowPubKey(ctx, v, r)
	case "unallowpubkey":
		v, r, e := pubkeyReason(params)
		if e != nil {
			return nil, e
		}
		return true, h.Store.UnallowPubKey(ctx, v, r)
	case "listbannedpubkeys":
		return h.Store.ListBannedPubKeys(ctx)
	case "listallowedpubkeys":
		return h.Store.ListAllowedPubKeys(ctx)
	case "createrole", "editrole":
		role, e := roleParams(params)
		if e != nil {
			return nil, e
		}
		if method == "createrole" {
			return true, h.Store.CreateRole(ctx, role)
		}
		return true, h.Store.EditRole(ctx, role)
	case "deleterole":
		id, e := oneString(params, "role id")
		if e != nil {
			return nil, e
		}
		return true, h.Store.DeleteRole(ctx, id)
	case "assignrole", "unassignrole":
		pubkey, roleID, e := pubkeyRole(params)
		if e != nil {
			return nil, e
		}
		if method == "assignrole" {
			return true, h.Store.AssignRole(ctx, pubkey, roleID)
		}
		return true, h.Store.UnassignRole(ctx, pubkey, roleID)
	case "listeventsneedingmoderation":
		return h.Store.ListEventsNeedingModeration(ctx)
	case "banevent":
		v, r, e := eventReason(params)
		if e != nil {
			return nil, e
		}
		return true, h.Store.BanEvent(ctx, v, r)
	case "allowevent":
		v, r, e := eventReason(params)
		if e != nil {
			return nil, e
		}
		return true, h.Store.AllowEvent(ctx, v, r)
	case "listbannedevents":
		return h.Store.ListBannedEvents(ctx)
	case "changerelayname":
		v, e := oneString(params, "name")
		if e != nil {
			return nil, e
		}
		return true, h.Store.ChangeRelayName(ctx, v)
	case "changerelaydescription":
		v, e := oneString(params, "description")
		if e != nil {
			return nil, e
		}
		return true, h.Store.ChangeRelayDescription(ctx, v)
	case "changerelayicon":
		v, e := oneString(params, "icon")
		if e != nil {
			return nil, e
		}
		return true, h.Store.ChangeRelayIcon(ctx, v)
	case "allowkind":
		k, e := oneInt(params, "kind")
		if e != nil {
			return nil, e
		}
		return true, h.Store.AllowKind(ctx, k)
	case "disallowkind":
		k, e := oneInt(params, "kind")
		if e != nil {
			return nil, e
		}
		return true, h.Store.DisallowKind(ctx, k)
	case "listallowedkinds":
		return h.Store.ListAllowedKinds(ctx)
	case "blockip":
		v, r, e := stringReason(params, "ip")
		if e != nil {
			return nil, e
		}
		return true, h.Store.BlockIP(ctx, v, r)
	case "unblockip":
		v, e := oneString(params, "ip")
		if e != nil {
			return nil, e
		}
		return true, h.Store.UnblockIP(ctx, v)
	case "listblockedips":
		return h.Store.ListBlockedIPs(ctx)
	default:
		return nil, fmt.Errorf("unsupported method %q", method)
	}
}

func write(w http.ResponseWriter, body response) {
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}
func absoluteURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if xf := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); xf != "" {
		scheme = strings.Split(xf, ",")[0]
	}
	return scheme + "://" + r.Host + r.URL.RequestURI()
}
func oneString(params []json.RawMessage, name string) (string, error) {
	if len(params) != 1 {
		return "", fmt.Errorf("%s parameter required", name)
	}
	var s string
	if err := json.Unmarshal(params[0], &s); err != nil || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("invalid %s", name)
	}
	return strings.TrimSpace(s), nil
}
func stringReason(params []json.RawMessage, name string) (string, string, error) {
	if len(params) < 1 || len(params) > 2 {
		return "", "", fmt.Errorf("%s parameter required", name)
	}
	v, err := oneString(params[:1], name)
	if err != nil {
		return "", "", err
	}
	reason := ""
	if len(params) == 2 {
		_ = json.Unmarshal(params[1], &reason)
		reason = strings.TrimSpace(reason)
	}
	return v, reason, nil
}
func pubkeyReason(params []json.RawMessage) (string, string, error) {
	pubkey, reason, err := stringReason(params, "pubkey")
	if err != nil {
		return "", "", err
	}
	parsed, err := nostr.PubKeyFromHex(pubkey)
	if err != nil {
		return "", "", fmt.Errorf("invalid pubkey")
	}
	return parsed.Hex(), reason, nil
}

func eventReason(params []json.RawMessage) (string, string, error) {
	id, reason, err := stringReason(params, "event id")
	if err != nil {
		return "", "", err
	}
	parsed, err := nostr.IDFromHex(id)
	if err != nil {
		return "", "", fmt.Errorf("invalid event id")
	}
	return parsed.Hex(), reason, nil
}

func roleParams(params []json.RawMessage) (Role, error) {
	if len(params) != 5 {
		return Role{}, fmt.Errorf("role parameters required")
	}
	values := make([]string, 4)
	for i, name := range []string{"role id", "label", "description", "color"} {
		if err := json.Unmarshal(params[i], &values[i]); err != nil {
			return Role{}, fmt.Errorf("invalid %s", name)
		}
		values[i] = strings.TrimSpace(values[i])
		if i < 2 && values[i] == "" {
			return Role{}, fmt.Errorf("invalid %s", name)
		}
	}
	order, err := oneInt(params[4:], "order")
	if err != nil {
		return Role{}, err
	}
	return Role{ID: values[0], Label: values[1], Description: values[2], Color: values[3], Order: order}, nil
}

func pubkeyRole(params []json.RawMessage) (string, string, error) {
	if len(params) != 2 {
		return "", "", fmt.Errorf("pubkey and role id parameters required")
	}
	pubkey, _, err := pubkeyReason(params[:1])
	if err != nil {
		return "", "", err
	}
	roleID, err := oneString(params[1:], "role id")
	if err != nil {
		return "", "", err
	}
	return pubkey, roleID, nil
}

func oneInt(params []json.RawMessage, name string) (int, error) {
	if len(params) != 1 {
		return 0, fmt.Errorf("%s parameter required", name)
	}
	var n int
	if err := json.Unmarshal(params[0], &n); err == nil {
		return n, nil
	}
	var f float64
	if err := json.Unmarshal(params[0], &f); err == nil && f == float64(int(f)) {
		return int(f), nil
	}
	var s string
	if err := json.Unmarshal(params[0], &s); err == nil {
		return strconv.Atoi(s)
	}
	return 0, fmt.Errorf("invalid %s", name)
}
