package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

// ResponsesTransportPolicy controls the streaming transport used by Responses
// providers. Auto prefers WebSocket when the model/provider advertises support,
// and falls back to SSE only when the WebSocket fails before a provider event.
type ResponsesTransportPolicy string

const (
	ResponsesTransportAuto      ResponsesTransportPolicy = "auto"
	ResponsesTransportSSE       ResponsesTransportPolicy = "sse"
	ResponsesTransportWebSocket ResponsesTransportPolicy = "websocket"
)

func parseResponsesTransportPolicy(raw string) (ResponsesTransportPolicy, error) {
	policy := ResponsesTransportPolicy(strings.ToLower(strings.TrimSpace(raw)))
	if policy == "" {
		policy = ResponsesTransportAuto
	}
	switch policy {
	case ResponsesTransportAuto, ResponsesTransportSSE, ResponsesTransportWebSocket:
		return policy, nil
	default:
		return "", fmt.Errorf("invalid Responses transport %q (want auto, sse, or websocket)", raw)
	}
}

func shouldFallbackResponsesWebSocket(policy ResponsesTransportPolicy, providerEventSeen bool, err error) bool {
	if policy != ResponsesTransportAuto || providerEventSeen || err == nil {
		return false
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

type responsesContinuation struct {
	lastRequest       responsesRequest
	lastResponseID    string
	lastResponseInput []map[string]any
}

type responsesTransportSession struct {
	mu           sync.Mutex
	identity     string
	connection   *websocket.Conn
	continuation *responsesContinuation
}

type responsesTransportState struct {
	mu       sync.Mutex
	sessions map[string]*responsesTransportSession
}

func (s *responsesTransportState) session(sessionID string) *responsesTransportSession {
	if strings.TrimSpace(sessionID) == "" {
		return &responsesTransportSession{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]*responsesTransportSession)
	}
	entry := s.sessions[sessionID]
	if entry == nil {
		entry = &responsesTransportSession{}
		s.sessions[sessionID] = entry
	}
	return entry
}

func cloneResponsesRequest(in responsesRequest) responsesRequest {
	raw, _ := json.Marshal(in)
	var out responsesRequest
	_ = json.Unmarshal(raw, &out)
	return out
}

func responsesRequestsMatchExceptInput(a, b responsesRequest) bool {
	a.Input = nil
	b.Input = nil
	a.PreviousResponseID = ""
	b.PreviousResponseID = ""
	rawA, errA := json.Marshal(a)
	rawB, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(rawA) == string(rawB)
}

func responsesInputEqual(a, b []map[string]any) bool {
	rawA, errA := json.Marshal(a)
	rawB, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(rawA) == string(rawB)
}

func prepareResponsesContinuation(full responsesRequest, cached *responsesContinuation) (responsesRequest, bool) {
	if cached == nil || strings.TrimSpace(cached.lastResponseID) == "" || !responsesRequestsMatchExceptInput(full, cached.lastRequest) {
		return full, false
	}
	baseline := make([]map[string]any, 0, len(cached.lastRequest.Input)+len(cached.lastResponseInput))
	baseline = append(baseline, cached.lastRequest.Input...)
	baseline = append(baseline, cached.lastResponseInput...)
	if len(full.Input) < len(baseline) || !responsesInputEqual(full.Input[:len(baseline)], baseline) {
		return full, false
	}
	request := cloneResponsesRequest(full)
	request.PreviousResponseID = cached.lastResponseID
	request.Input = append([]map[string]any(nil), full.Input[len(baseline):]...)
	return request, true
}

func responseInputForContinuation(result ProviderResult) []map[string]any {
	// buildRequest currently projects assistant history as role/content. Mirror
	// that canonical shape so the next full request can prove an unchanged prefix.
	return []map[string]any{{"role": "assistant", "content": result.Text}}
}

func responsesTransportIdentity(cfg responsesHTTPConfig, model string) string {
	req, _ := http.NewRequest(http.MethodGet, cfg.Endpoint, nil)
	if cfg.ApplyAuth != nil {
		cfg.ApplyAuth(req)
	}
	auth := req.Header.Get("Authorization") + "\x00" + req.Header.Get("api-key")
	sum := sha256.Sum256([]byte(auth))
	return cfg.Endpoint + "\x00" + model + "\x00" + hex.EncodeToString(sum[:])
}

func resetResponsesTransportSession(entry *responsesTransportSession, identity string) {
	if entry.connection != nil {
		_ = entry.connection.Close(websocket.StatusNormalClosure, "transport reset")
	}
	entry.connection = nil
	entry.continuation = nil
	entry.identity = identity
}

func streamResponsesRequest(
	ctx context.Context,
	turn Turn,
	full responsesRequest,
	cfg responsesHTTPConfig,
	policy ResponsesTransportPolicy,
	supportsWebSocket bool,
	supportsContinuation bool,
	state *responsesTransportState,
	emit ProviderStreamEventSink,
) (ProviderResult, error) {
	resolved, err := parseResponsesTransportPolicy(string(policy))
	if err != nil {
		return ProviderResult{}, err
	}
	if resolved == ResponsesTransportWebSocket && !supportsWebSocket {
		return ProviderResult{}, fmt.Errorf("Responses websocket transport is not supported by this provider/model")
	}

	full.Stream = true
	sessionID := strings.TrimSpace(turn.SessionID)
	if supportsContinuation && sessionID != "" {
		full.Store = true
	}
	entry := state.session(sessionID)
	entry.mu.Lock()
	defer entry.mu.Unlock()

	identity := responsesTransportIdentity(cfg, full.Model)
	if entry.identity != identity {
		resetResponsesTransportSession(entry, identity)
	}

	request := full
	continued := false
	if supportsContinuation && sessionID != "" {
		request, continued = prepareResponsesContinuation(full, entry.continuation)
	}
	// A cached continuation is single-use. A mismatched full request invalidates
	// it immediately; a failed attempt must never resurrect stale response state.
	entry.continuation = nil

	commit := func(result ProviderResult) {
		if supportsContinuation && sessionID != "" && strings.TrimSpace(result.responseID) != "" {
			entry.continuation = &responsesContinuation{
				lastRequest:       cloneResponsesRequest(full),
				lastResponseID:    result.responseID,
				lastResponseInput: responseInputForContinuation(result),
			}
		}
	}

	if resolved == ResponsesTransportSSE || !supportsWebSocket {
		if entry.connection != nil {
			_ = entry.connection.Close(websocket.StatusNormalClosure, "sse selected")
			entry.connection = nil
		}
		result, streamErr := executeResponsesSSE(ctx, request, cfg, emit)
		if streamErr == nil {
			commit(result)
		}
		return result, streamErr
	}

	result, providerEventSeen, wsErr := executeResponsesWebSocket(ctx, request, cfg, entry, sessionID != "", emit)
	if wsErr == nil {
		commit(result)
		return result, nil
	}
	if entry.connection != nil {
		_ = entry.connection.Close(websocket.StatusInternalError, "websocket stream failed")
		entry.connection = nil
	}
	if !shouldFallbackResponsesWebSocket(resolved, providerEventSeen, wsErr) {
		return result, wsErr
	}

	// Reuse the same semantic request for this attempt. In particular, a valid
	// previous_response_id remains valid across the WebSocket -> SSE fallback.
	result, sseErr := executeResponsesSSE(ctx, request, cfg, emit)
	if sseErr == nil {
		commit(result)
		return result, nil
	}
	if continued {
		return result, fmt.Errorf("Responses websocket failed (%v); SSE continuation fallback failed: %w", wsErr, sseErr)
	}
	return result, fmt.Errorf("Responses websocket failed (%v); SSE fallback failed: %w", wsErr, sseErr)
}

func executeResponsesSSE(ctx context.Context, request responsesRequest, cfg responsesHTTPConfig, emit ProviderStreamEventSink) (ProviderResult, error) {
	request.Stream = true
	resp, err := executeResponsesRequest(ctx, request, cfg)
	if err != nil {
		return ProviderResult{}, err
	}
	defer resp.Body.Close()
	return consumeResponsesStream(resp.Body, emit)
}

func responsesWebSocketURL(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "wss", "ws":
	default:
		return "", fmt.Errorf("Responses websocket endpoint has unsupported scheme %q", u.Scheme)
	}
	return u.String(), nil
}

func responsesWebSocketHeaders(cfg responsesHTTPConfig) http.Header {
	req, _ := http.NewRequest(http.MethodGet, cfg.Endpoint, nil)
	if cfg.ApplyAuth != nil {
		cfg.ApplyAuth(req)
	}
	headers := req.Header.Clone()
	headers.Set("OpenAI-Beta", "responses_websockets=2026-02-06")
	return headers
}

type responsesWebSocketSSEReader struct {
	ctx               context.Context
	conn              *websocket.Conn
	pending           []byte
	providerEventSeen bool
}

func (r *responsesWebSocketSSEReader) Read(p []byte) (int, error) {
	for len(r.pending) == 0 {
		messageType, raw, err := r.conn.Read(r.ctx)
		if err != nil {
			return 0, err
		}
		if messageType != websocket.MessageText && messageType != websocket.MessageBinary {
			continue
		}
		var envelope struct {
			Type string `json:"type"`
		}
		// A server error is an explicit safe rejection, not accepted output; auto
		// may retry it over SSE. Any other provider event makes fallback ambiguous.
		if json.Unmarshal(raw, &envelope) != nil || envelope.Type != "error" {
			r.providerEventSeen = true
		}
		r.pending = make([]byte, 0, len(raw)+8)
		r.pending = append(r.pending, "data: "...)
		r.pending = append(r.pending, raw...)
		r.pending = append(r.pending, '\n', '\n')
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func executeResponsesWebSocket(
	ctx context.Context,
	request responsesRequest,
	cfg responsesHTTPConfig,
	entry *responsesTransportSession,
	keepConnection bool,
	emit ProviderStreamEventSink,
) (ProviderResult, bool, error) {
	conn := entry.connection
	if conn == nil {
		endpoint, err := responsesWebSocketURL(cfg.Endpoint)
		if err != nil {
			return ProviderResult{}, false, err
		}
		conn, _, err = websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: cfg.Client, HTTPHeader: responsesWebSocketHeaders(cfg)})
		if err != nil {
			return ProviderResult{}, false, fmt.Errorf("Responses websocket connect: %w", err)
		}
		conn.SetReadLimit(maxProviderSSEEventBytes)
		if keepConnection {
			entry.connection = conn
		}
	}
	if !keepConnection {
		defer conn.Close(websocket.StatusNormalClosure, "response complete")
	}

	wire := cloneResponsesRequest(request)
	wire.Stream = false
	payload := struct {
		Type string `json:"type"`
		responsesRequest
	}{Type: "response.create", responsesRequest: wire}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ProviderResult{}, false, fmt.Errorf("Responses websocket request encode: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		return ProviderResult{}, false, fmt.Errorf("Responses websocket write: %w", err)
	}
	reader := &responsesWebSocketSSEReader{ctx: ctx, conn: conn}
	result, err := consumeResponsesStream(reader, emit)
	if err != nil {
		return result, reader.providerEventSeen, fmt.Errorf("Responses websocket stream: %w", err)
	}
	return result, reader.providerEventSeen, nil
}

var _ io.Reader = (*responsesWebSocketSSEReader)(nil)
