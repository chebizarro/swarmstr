package talk

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"metiq/internal/autoreply"
	"metiq/internal/realtimevoice"
)

// BrowserSessionProvider is the optional realtimevoice.Provider capability
// required by browser-owned transports (webrtc / provider-websocket): the
// provider mints a session the browser connects to directly. No metiq-wired
// provider implements it yet, so talk.client.create is honestly unavailable
// until one does (accepted deviation, tracked as a follow-up).
type BrowserSessionProvider interface {
	CreateBrowserSession(ctx context.Context, cfg BrowserSessionConfig) (map[string]any, error)
}

// BrowserSessionConfig carries the browser-session request parameters.
type BrowserSessionConfig struct {
	Voice    string
	Language string
	Model    string
}

// TranscriptEntry is one appended transcript line on a client session.
type TranscriptEntry struct {
	Role  string `json:"role"`
	Text  string `json:"text"`
	Final bool   `json:"final"`
}

// ToolCallInput is the realtime agent-consult tool payload bridged into a run.
type ToolCallInput struct {
	SessionID  string
	Tool       string
	ToolCallID string
	AgentID    string
	Arguments  json.RawMessage
}

// ToolCallBridge dispatches a bridged agent-consult run and returns its run id.
// The daemon supplies this, backed by the live agent runtime.
type ToolCallBridge func(ctx context.Context, in ToolCallInput) (string, error)

type clientSession struct {
	id          string
	transport   string
	provider    string
	agentID     string
	transcript  []TranscriptEntry
	activeRunID string
	closed      bool
}

// ClientStore tracks client-owned (browser-origin) voice session records.
type ClientStore struct {
	mu       sync.Mutex
	sessions map[string]*clientSession
	seq      int

	voice    *realtimevoice.Registry
	steering *autoreply.SteeringMailboxRegistry
}

// NewClientStore constructs a client-session store. voice/steering may be nil.
func NewClientStore(voice *realtimevoice.Registry, steering *autoreply.SteeringMailboxRegistry) *ClientStore {
	return &ClientStore{sessions: map[string]*clientSession{}, voice: voice, steering: steering}
}

// ClientCreateInput carries resolved talk.client.create parameters.
type ClientCreateInput struct {
	SessionID string
	Transport string
	Provider  string
	Voice     string
	Language  string
	Model     string
	AgentID   string
}

// Create mints (or resumes) a client-owned voice session record. Browser-owned
// realtime transports require a realtimevoice provider that supports
// createBrowserSession; when none is registered the call is honestly
// unavailable rather than fabricating a session.
func (s *ClientStore) Create(ctx context.Context, in ClientCreateInput) (map[string]any, error) {
	// Resume an existing record when the caller supplies a known id.
	if in.SessionID != "" {
		s.mu.Lock()
		if existing, ok := s.sessions[in.SessionID]; ok && !existing.closed {
			out := map[string]any{
				"sessionId": existing.id, "transport": existing.transport,
				"provider": existing.provider, "resumed": true,
			}
			s.mu.Unlock()
			return out, nil
		}
		s.mu.Unlock()
	}

	var browser map[string]any
	switch in.Transport {
	case "webrtc", "provider-websocket":
		provider, err := s.browserProvider(in.Provider)
		if err != nil {
			return nil, err
		}
		browser, err = provider.CreateBrowserSession(ctx, BrowserSessionConfig{
			Voice: in.Voice, Language: in.Language, Model: in.Model,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: create browser session: %v", ErrUnavailable, err)
		}
	default:
		return nil, fmt.Errorf("unsupported client transport %q", in.Transport)
	}

	s.mu.Lock()
	s.seq++
	id := in.SessionID
	if id == "" {
		id = fmt.Sprintf("talk-client-%d", s.seq)
	}
	sess := &clientSession{
		id: id, transport: in.Transport, provider: in.Provider,
		agentID: in.AgentID, transcript: []TranscriptEntry{},
	}
	s.sessions[id] = sess
	s.mu.Unlock()

	out := map[string]any{"sessionId": id, "transport": in.Transport, "provider": in.Provider, "resumed": false}
	if browser != nil {
		out["browserSession"] = browser
	}
	return out, nil
}

func (s *ClientStore) browserProvider(id string) (BrowserSessionProvider, error) {
	if s.voice == nil {
		return nil, fmt.Errorf("%w: no realtimevoice registry wired", ErrUnavailable)
	}
	var provider realtimevoice.Provider
	if id != "" {
		p, ok := s.voice.Get(id)
		if !ok {
			return nil, fmt.Errorf("%w: unknown realtime voice provider %q", ErrUnavailable, id)
		}
		provider = p
	} else {
		p, err := s.voice.Default()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		provider = p
	}
	bp, ok := provider.(BrowserSessionProvider)
	if !ok {
		return nil, fmt.Errorf("%w: realtime voice provider %q does not support browser-owned sessions", ErrUnavailable, provider.ID())
	}
	return bp, nil
}

// Transcript appends a transcript entry to a client session.
func (s *ClientStore) Transcript(sessionID string, entry TranscriptEntry) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok || sess.closed {
		return nil, fmt.Errorf("unknown talk client session %q", sessionID)
	}
	sess.transcript = append(sess.transcript, entry)
	return map[string]any{"sessionId": sessionID, "entries": len(sess.transcript)}, nil
}

// Close closes a client-owned session record.
func (s *ClientStore) Close(sessionID string) (map[string]any, error) {
	s.mu.Lock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("unknown talk client session %q", sessionID)
	}
	sess.closed = true
	delete(s.sessions, sessionID)
	s.mu.Unlock()
	if s.steering != nil {
		s.steering.Delete(sessionID)
	}
	return map[string]any{"sessionId": sessionID, "closed": true}, nil
}

// ToolCall bridges the realtime agent-consult tool into an agent run.
func (s *ClientStore) ToolCall(ctx context.Context, in ToolCallInput, bridge ToolCallBridge) (map[string]any, error) {
	s.mu.Lock()
	sess, ok := s.sessions[in.SessionID]
	if !ok || sess.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("unknown talk client session %q", in.SessionID)
	}
	if sess.agentID != "" && in.AgentID == "" {
		in.AgentID = sess.agentID
	}
	s.mu.Unlock()

	if bridge == nil {
		return nil, fmt.Errorf("%w: agent runtime not configured", ErrUnavailable)
	}
	runID, err := bridge(ctx, in)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if cur, ok := s.sessions[in.SessionID]; ok {
		cur.activeRunID = runID
	}
	s.mu.Unlock()

	return map[string]any{"sessionId": in.SessionID, "toolCallId": in.ToolCallID, "runId": runID}, nil
}

// Steer enqueues steering text for the client session's active run.
func (s *ClientStore) Steer(sessionID, text string) (map[string]any, error) {
	s.mu.Lock()
	sess, ok := s.sessions[sessionID]
	if !ok || sess.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("unknown talk client session %q", sessionID)
	}
	target := sess.activeRunID
	s.mu.Unlock()

	if s.steering == nil {
		return nil, fmt.Errorf("%w: steering mailboxes not configured", ErrUnavailable)
	}
	key := target
	if key == "" {
		key = sessionID
	}
	accepted := s.steering.Get(key).Enqueue(autoreply.SteeringMessage{Text: text, Source: "talk-client"})
	return map[string]any{"sessionId": sessionID, "accepted": accepted, "runId": target}, nil
}
