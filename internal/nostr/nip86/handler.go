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

	"metiq/internal/nostr/nip98"
)

const ContentType = "application/nostr+json+rpc"

var SupportedMethods = []string{"supportedmethods", "banpubkey", "allowpubkey", "listbannedpubkeys", "listallowedpubkeys", "banevent", "allowevent", "listbannedevents", "changerelayname", "changerelaydescription", "changerelayicon", "allowkind", "disallowkind", "listallowedkinds", "listdisallowedkinds", "blockip", "unblockip", "listblockedips"}

type Handler struct {
	Store    ManagementStore
	RelayURL string
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
	return &Handler{Store: store, RelayURL: strings.TrimSpace(relayURL)}
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
	if _, err := nip98.VerifyPayloadRequired(r.Header.Get("Authorization"), r.Method, u, raw); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
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
		v, r, e := stringReason(params, "pubkey")
		if e != nil {
			return nil, e
		}
		return true, h.Store.BanPubKey(ctx, v, r)
	case "allowpubkey":
		v, r, e := stringReason(params, "pubkey")
		if e != nil {
			return nil, e
		}
		return true, h.Store.AllowPubKey(ctx, v, r)
	case "listbannedpubkeys":
		return h.Store.ListBannedPubKeys(ctx)
	case "listallowedpubkeys":
		return h.Store.ListAllowedPubKeys(ctx)
	case "banevent":
		v, r, e := stringReason(params, "event id")
		if e != nil {
			return nil, e
		}
		return true, h.Store.BanEvent(ctx, v, r)
	case "allowevent":
		v, r, e := stringReason(params, "event id")
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
	case "listdisallowedkinds":
		return h.Store.ListDisallowedKinds(ctx)
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
