package board

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"regexp"
	"time"
)

// View tickets are short-lived HMAC-signed capability tokens that authorize
// one widget instance — at an exact revision and view generation — to call
// the ticket-scoped board methods (board.prompt.authorize, board.data.read,
// board.action, the ticket variant of board.event) and to fetch its sandboxed
// frame from the board frame host. The wire format mirrors OpenClaw
// src/gateway/board-view-ticket.ts: "v1.<base64url claims>.<base64url hmac>"
// signed with a per-process random secret, so tickets never survive a daemon
// restart and can never be forged or extended by widget code.

const (
	// ViewTicketTTL bounds how long a minted ticket authorizes its widget
	// view before the client must refetch board.get for a fresh one.
	ViewTicketTTL = 2 * time.Minute
	// HTTPPathPrefix is where the board widget frame host serves pinned
	// widget HTML (ticket-validated; see the frame host follow-up slice).
	HTTPPathPrefix = "/__metiq__/board/"
	// MaxTicketLength bounds tickets accepted on the wire.
	MaxTicketLength = 2048

	ticketScope = "board-widget-view"
)

var (
	viewGenerationPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
	ticketNoncePattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`)
)

type ticketClaims struct {
	SessionKey     string `json:"sessionKey"`
	Name           string `json:"name"`
	Revision       int    `json:"revision"`
	ViewGeneration string `json:"viewGeneration"`
	ExpiresAtMs    int64  `json:"expiresAtMs"`
	Nonce          string `json:"nonce"`
}

func (c ticketClaims) valid() bool {
	return c.SessionKey != "" && len(c.SessionKey) <= 512 &&
		c.Name != "" && len(c.Name) <= 64 &&
		c.Revision >= 1 &&
		viewGenerationPattern.MatchString(c.ViewGeneration) &&
		ticketNoncePattern.MatchString(c.Nonce)
}

func newTicketSecret() []byte {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	return secret
}

func newTicketNonce() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func signTicketPayload(secret []byte, payload string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(ticketScope))
	mac.Write([]byte{0})
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func mintViewTicket(secret []byte, claims ticketClaims) (string, error) {
	if !claims.valid() {
		return "", errInvalid("invalid board view ticket binding")
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", errInvalid("invalid board view ticket binding")
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return "v1." + payload + "." + signTicketPayload(secret, payload), nil
}

func verifyViewTicket(secret []byte, ticket string, now time.Time) (ticketClaims, bool) {
	if ticket == "" || len(ticket) > MaxTicketLength {
		return ticketClaims{}, false
	}
	const prefix = "v1."
	if len(ticket) <= len(prefix) || ticket[:len(prefix)] != prefix {
		return ticketClaims{}, false
	}
	rest := ticket[len(prefix):]
	dot := -1
	for i := 0; i < len(rest); i++ {
		if rest[i] == '.' {
			dot = i
			break
		}
	}
	if dot <= 0 || dot == len(rest)-1 {
		return ticketClaims{}, false
	}
	payload, signature := rest[:dot], rest[dot+1:]
	expected := signTicketPayload(secret, payload)
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return ticketClaims{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return ticketClaims{}, false
	}
	var claims ticketClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		return ticketClaims{}, false
	}
	if !claims.valid() || claims.ExpiresAtMs <= now.UnixMilli() {
		return ticketClaims{}, false
	}
	return claims, true
}

// BuildFrameURL returns the ticketed widget frame path served by the board
// frame host.
func BuildFrameURL(sessionKey, name, ticket string) string {
	return HTTPPathPrefix + url.PathEscape(sessionKey) + "/" + url.PathEscape(name) + "/index.html?bt=" + url.QueryEscape(ticket)
}

// AuthorizedView is the widget identity and byte-frozen document resolved
// from a valid, fresh view ticket.
type AuthorizedView struct {
	SessionKey string
	Name       string
	HTML       string
	Revision   int
	GrantState string
	Declared   *Declared
}

// HasGrantedTool reports whether the capability was both declared by the
// widget document and granted by the operator. Widgets with grantState
// "none" declared nothing, so every capability check fails closed.
func (v AuthorizedView) HasGrantedTool(tool string) bool {
	if v.GrantState != GrantGranted || v.Declared == nil || tool == "" {
		return false
	}
	for _, declared := range v.Declared.Tools {
		if declared == tool {
			return true
		}
	}
	return false
}

// GetSnapshotWithTickets returns the board snapshot with a fresh view ticket
// minted for every renderable html widget: grantState none or granted, with
// a pinned document matching the widget revision. Pending and rejected
// widgets never receive tickets.
func (s *Store) GetSnapshotWithTickets(sessionKey string) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.boards[sessionKey]
	if !ok {
		return emptySnapshot(sessionKey)
	}
	s.revokeStalePluginGrantsLocked(b)
	snapshot := CloneSnapshot(b.snapshot)
	expiresAtMs := s.now().Add(ViewTicketTTL).UnixMilli()
	for i := range snapshot.Widgets {
		w := &snapshot.Widgets[i]
		if w.ContentKind != ContentKindHTML {
			continue
		}
		if w.GrantState != GrantNone && w.GrantState != GrantGranted {
			continue
		}
		doc := b.documents[w.Name]
		if doc == nil || doc.Revision != w.Revision {
			continue
		}
		ticket, err := mintViewTicket(s.ticketSecret, ticketClaims{
			SessionKey:     sessionKey,
			Name:           w.Name,
			Revision:       w.Revision,
			ViewGeneration: doc.ViewGeneration,
			ExpiresAtMs:    expiresAtMs,
			Nonce:          newTicketNonce(),
		})
		if err != nil {
			continue
		}
		w.ViewTicket = ticket
		w.ViewTicketTTLMs = int(ViewTicketTTL / time.Millisecond)
		w.ViewGeneration = doc.ViewGeneration
		w.FrameURL = BuildFrameURL(sessionKey, w.Name, ticket)
	}
	return snapshot
}

// ResolveViewTicket authorizes a ticket against current store state. A ticket
// goes stale — even before its TTL — the moment the widget is re-put (new
// revision/view generation), its grant is rejected, or the board is deleted.
func (s *Store) ResolveViewTicket(ticket string) (AuthorizedView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	claims, ok := verifyViewTicket(s.ticketSecret, ticket, s.now())
	if !ok {
		return AuthorizedView{}, errInvalid("board widget view ticket is invalid")
	}
	b, okBoard := s.boards[claims.SessionKey]
	if !okBoard {
		return AuthorizedView{}, errInvalid("board widget view ticket is stale")
	}
	s.revokeStalePluginGrantsLocked(b)
	doc := b.documents[claims.Name]
	if doc == nil ||
		(doc.GrantState != GrantNone && doc.GrantState != GrantGranted) ||
		doc.Revision != claims.Revision ||
		doc.ViewGeneration != claims.ViewGeneration {
		return AuthorizedView{}, errInvalid("board widget view ticket is stale")
	}
	return AuthorizedView{
		SessionKey: claims.SessionKey,
		Name:       claims.Name,
		HTML:       doc.HTML,
		Revision:   doc.Revision,
		GrantState: doc.GrantState,
		Declared:   cloneDeclared(doc.Declared),
	}, nil
}
