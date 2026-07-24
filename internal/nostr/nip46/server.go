package nip46

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
)

var ErrDuplicate = errors.New("duplicate NIP-46 request")

type AuthChallengeError struct{ URL string }

func (e AuthChallengeError) Error() string { return e.URL }

type ConnectAuthorizer func(context.Context, nostr.PubKey, string, PermissionSet, ClientMetadata) (PermissionSet, error)
type OperationAuthorizer func(context.Context, nostr.PubKey, string, nostr.Kind) error

type ServerOptions struct {
	Handler            nostr.Keyer // remote-signer channel identity
	User               nostr.Keyer // user identity; may be distinct from Handler
	Relays             []string
	AuthorizeConnect   ConnectAuthorizer
	AuthorizeOperation OperationAuthorizer
}

type serverSession struct {
	permissions PermissionSet
	requestIDs  map[string]struct{}
}

// Server handles NIP-46 request events. Relay subscription/publishing remains
// with the caller so it can use the runtime's managed, reconnecting hub.
type Server struct {
	mu                 sync.Mutex
	handler            nostr.Keyer
	user               nostr.Keyer
	handlerPub         nostr.PubKey
	userPub            nostr.PubKey
	relays             []string
	authorizeConnect   ConnectAuthorizer
	authorizeOperation OperationAuthorizer
	sessions           map[nostr.PubKey]*serverSession
	usedSecrets        map[string]struct{}
	seenEvents         map[nostr.ID]struct{}
	seenOrder          []nostr.ID
	now                func() time.Time
}

func NewServer(ctx context.Context, opts ServerOptions) (*Server, error) {
	if opts.Handler == nil || opts.User == nil {
		return nil, fmt.Errorf("NIP-46 handler and user keyers are required")
	}
	if opts.AuthorizeConnect == nil {
		return nil, fmt.Errorf("NIP-46 connect authorizer is required")
	}
	handlerPub, err := opts.Handler.GetPublicKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve NIP-46 handler pubkey: %w", err)
	}
	userPub, err := opts.User.GetPublicKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve NIP-46 user pubkey: %w", err)
	}
	relays, err := normalizeRelayURLs(opts.Relays)
	if err != nil {
		return nil, err
	}
	return &Server{handler: opts.Handler, user: opts.User, handlerPub: handlerPub, userPub: userPub, relays: relays,
		authorizeConnect: opts.AuthorizeConnect, authorizeOperation: opts.AuthorizeOperation,
		sessions: map[nostr.PubKey]*serverSession{}, usedSecrets: map[string]struct{}{}, seenEvents: map[nostr.ID]struct{}{}, now: time.Now}, nil
}

func (s *Server) HandlerPubKey() nostr.PubKey { return s.handlerPub }
func (s *Server) UserPubKey() nostr.PubKey    { return s.userPub }

func (s *Server) HandleEvent(ctx context.Context, event nostr.Event) (Request, nostr.Event, error) {
	var request Request
	if err := validateProtocolEvent(event, nostr.ZeroPK, s.handlerPub, s.now()); err != nil {
		return request, nostr.Event{}, err
	}
	s.mu.Lock()
	if _, ok := s.seenEvents[event.ID]; ok {
		s.mu.Unlock()
		return request, nostr.Event{}, ErrDuplicate
	}
	s.seenEvents[event.ID] = struct{}{}
	s.seenOrder = append(s.seenOrder, event.ID)
	if len(s.seenOrder) > 4096 {
		delete(s.seenEvents, s.seenOrder[0])
		s.seenOrder = s.seenOrder[1:]
	}
	s.mu.Unlock()
	plaintext, err := s.handler.Decrypt(ctx, event.Content, event.PubKey)
	if err != nil {
		return request, nostr.Event{}, fmt.Errorf("decrypt NIP-46 request: %w", err)
	}
	if err := json.Unmarshal([]byte(plaintext), &request); err != nil {
		return request, nostr.Event{}, fmt.Errorf("decode NIP-46 request: %w", err)
	}
	if request.ID == "" || !knownMethod(request.Method) {
		return request, nostr.Event{}, fmt.Errorf("invalid NIP-46 request")
	}

	s.mu.Lock()
	session := s.sessions[event.PubKey]
	if session != nil {
		if _, ok := session.requestIDs[request.ID]; ok {
			s.mu.Unlock()
			return request, nostr.Event{}, ErrDuplicate
		}
		session.requestIDs[request.ID] = struct{}{}
	}
	s.mu.Unlock()

	result, callErr, logout := s.execute(ctx, event.PubKey, request, session)
	response := Response{ID: request.ID, Result: result}
	var challenge AuthChallengeError
	if errors.As(callErr, &challenge) {
		response.Result = "auth_url"
		response.Error = challenge.URL
	} else if callErr != nil {
		response.Result = ""
		response.Error = callErr.Error()
	}
	responseEvent, err := s.makeResponse(ctx, event.PubKey, response)
	if err != nil {
		return request, nostr.Event{}, err
	}
	if logout && callErr == nil {
		s.mu.Lock()
		delete(s.sessions, event.PubKey)
		s.mu.Unlock()
	}
	return request, responseEvent, nil
}

func (s *Server) execute(ctx context.Context, client nostr.PubKey, request Request, session *serverSession) (string, error, bool) {
	if request.Method == MethodConnect {
		if len(request.Params) < 1 || request.Params[0] != s.handlerPub.Hex() {
			return "", fmt.Errorf("connect target does not match remote signer"), false
		}
		secret, requestedRaw := "", ""
		metadata := ClientMetadata{}
		if len(request.Params) > 1 {
			secret = request.Params[1]
		}
		if len(request.Params) > 2 {
			requestedRaw = request.Params[2]
		}
		if len(request.Params) > 3 && request.Params[3] != "" {
			if err := json.Unmarshal([]byte(request.Params[3]), &metadata); err != nil {
				return "", fmt.Errorf("invalid client metadata"), false
			}
		}
		requested, err := ParsePermissions(requestedRaw)
		if err != nil {
			return "", err, false
		}
		s.mu.Lock()
		_, reused := s.usedSecrets[secret]
		s.mu.Unlock()
		if secret != "" && reused {
			return "", fmt.Errorf("connection secret already used"), false
		}
		granted, err := s.authorizeConnect(ctx, client, secret, requested, metadata)
		if err != nil {
			return "", err, false
		}
		s.mu.Lock()
		s.sessions[client] = &serverSession{permissions: granted, requestIDs: map[string]struct{}{request.ID: {}}}
		if secret != "" {
			s.usedSecrets[secret] = struct{}{}
		}
		s.mu.Unlock()
		return "ack", nil, false
	}
	if session == nil {
		return "", fmt.Errorf("client is not connected"), false
	}

	kind := nostr.Kind(0)
	var unsigned nostr.Event
	if request.Method == MethodSignEvent {
		if len(request.Params) != 1 || json.Unmarshal([]byte(request.Params[0]), &unsigned) != nil {
			return "", fmt.Errorf("sign_event requires one unsigned event"), false
		}
		kind = unsigned.Kind
	}
	if request.Method != MethodPing && request.Method != MethodGetPublicKey && request.Method != MethodSwitchRelays && request.Method != MethodLogout && !session.permissions.Allows(request.Method, kind) {
		return "", fmt.Errorf("permission denied for %s", request.Method), false
	}
	if s.authorizeOperation != nil {
		if err := s.authorizeOperation(ctx, client, request.Method, kind); err != nil {
			return "", err, false
		}
	}

	switch request.Method {
	case MethodPing:
		return "pong", nil, false
	case MethodGetPublicKey:
		return s.userPub.Hex(), nil, false
	case MethodSwitchRelays:
		if len(s.relays) == 0 {
			return "null", nil, false
		}
		result, err := encodeJSON(s.relays)
		return result, err, false
	case MethodLogout:
		return "ack", nil, true
	case MethodSignEvent:
		unsigned.ID, unsigned.PubKey, unsigned.Sig = nostr.ID{}, nostr.PubKey{}, [64]byte{}
		if err := s.user.SignEvent(ctx, &unsigned); err != nil {
			return "", err, false
		}
		if unsigned.PubKey != s.userPub || !unsigned.CheckID() || !unsigned.VerifySignature() {
			return "", fmt.Errorf("user keyer returned invalid signature"), false
		}
		result, err := encodeJSON(unsigned)
		return result, err, false
	case MethodNIP44Encrypt, MethodNIP44Decrypt, MethodNIP04Encrypt, MethodNIP04Decrypt:
		if len(request.Params) != 2 {
			return "", fmt.Errorf("%s requires pubkey and payload", request.Method), false
		}
		peer, err := nostr.PubKeyFromHex(request.Params[0])
		if err != nil {
			return "", fmt.Errorf("invalid peer pubkey"), false
		}
		switch request.Method {
		case MethodNIP44Encrypt:
			result, err := s.user.Encrypt(ctx, request.Params[1], peer)
			return result, err, false
		case MethodNIP44Decrypt:
			result, err := s.user.Decrypt(ctx, request.Params[1], peer)
			return result, err, false
		case MethodNIP04Encrypt:
			op, ok := s.user.(interface {
				EncryptNIP04(context.Context, string, nostr.PubKey) (string, error)
			})
			if !ok {
				return "", fmt.Errorf("user keyer does not support NIP-04 encryption"), false
			}
			result, err := op.EncryptNIP04(ctx, request.Params[1], peer)
			return result, err, false
		case MethodNIP04Decrypt:
			op, ok := s.user.(interface {
				DecryptNIP04(context.Context, string, nostr.PubKey) (string, error)
			})
			if !ok {
				return "", fmt.Errorf("user keyer does not support NIP-04 decryption"), false
			}
			result, err := op.DecryptNIP04(ctx, request.Params[1], peer)
			return result, err, false
		}
	}
	return "", fmt.Errorf("unsupported NIP-46 method"), false
}

func (s *Server) makeResponse(ctx context.Context, client nostr.PubKey, response Response) (nostr.Event, error) {
	payload, err := encodeJSON(response)
	if err != nil {
		return nostr.Event{}, err
	}
	content, err := s.handler.Encrypt(ctx, payload, client)
	if err != nil {
		return nostr.Event{}, fmt.Errorf("encrypt NIP-46 response: %w", err)
	}
	event := nostr.Event{Kind: Kind, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"p", client.Hex()}}, Content: content}
	if err := s.handler.SignEvent(ctx, &event); err != nil {
		return nostr.Event{}, fmt.Errorf("sign NIP-46 response: %w", err)
	}
	return event, nil
}

// CompleteAuthorization emits the terminal response for a prior auth_url
// challenge using the same request ID.
func (s *Server) CompleteAuthorization(ctx context.Context, client nostr.PubKey, requestID, result string, callErr error) (nostr.Event, error) {
	if requestID == "" {
		return nostr.Event{}, fmt.Errorf("request ID is required")
	}
	s.mu.Lock()
	_, ok := s.sessions[client]
	s.mu.Unlock()
	if !ok {
		return nostr.Event{}, fmt.Errorf("client is not connected")
	}
	response := Response{ID: requestID, Result: result}
	if callErr != nil {
		response.Result = ""
		response.Error = callErr.Error()
	}
	return s.makeResponse(ctx, client, response)
}

// HandleNostrConnectURL accepts a client-generated nostrconnect:// token and
// returns the secret-bearing response that establishes the connection.
func (s *Server) HandleNostrConnectURL(ctx context.Context, rawURL string) (NostrConnectToken, nostr.Event, error) {
	token, err := ParseNostrConnectURL(rawURL)
	if err != nil {
		return token, nostr.Event{}, err
	}
	granted, err := s.authorizeConnect(ctx, token.ClientPubKey, token.Secret, token.Permissions, token.Metadata)
	if err != nil {
		return token, nostr.Event{}, err
	}
	s.mu.Lock()
	if _, used := s.usedSecrets[token.Secret]; used {
		s.mu.Unlock()
		return token, nostr.Event{}, fmt.Errorf("connection secret already used")
	}
	s.usedSecrets[token.Secret] = struct{}{}
	s.sessions[token.ClientPubKey] = &serverSession{permissions: granted, requestIDs: map[string]struct{}{}}
	s.mu.Unlock()
	response := Response{ID: "nostrconnect", Result: token.Secret}
	event, err := s.makeResponse(ctx, token.ClientPubKey, response)
	return token, event, err
}

func ParseNostrConnectURI(raw string) (*url.URL, error) {
	if _, err := ParseNostrConnectURL(raw); err != nil {
		return nil, err
	}
	return url.Parse(raw)
}
