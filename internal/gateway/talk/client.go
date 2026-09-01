package talk

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"metiq/internal/autoreply"
	browserpkg "metiq/internal/browser"
	"metiq/internal/realtimevoice"
)

// BrowserSessionProvider and BrowserSessionConfig retain the talk package names
// while sharing the provider-neutral contract from internal/browser.
type BrowserSessionProvider = browserpkg.SessionProvider
type BrowserSessionConfig = browserpkg.SessionConfig
type BrowserSession = browserpkg.Session

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

	var browserSession browserpkg.Session
	var resolvedProvider realtimevoice.Provider
	switch in.Transport {
	case browserpkg.TransportWebRTC, browserpkg.TransportProviderWebSocket:
		provider, browserProvider, err := s.browserProvider(in.Provider, in.Transport)
		if err != nil {
			return nil, err
		}
		resolvedProvider = provider
		browserSession, err = browserProvider.CreateBrowserSession(ctx, BrowserSessionConfig{
			Transport: in.Transport, Voice: in.Voice, Language: in.Language, Model: in.Model,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: create browser session: %v", ErrUnavailable, err)
		}
		if browserSession == nil {
			return nil, fmt.Errorf("%w: provider %q returned an empty browser session", ErrUnavailable, provider.ID())
		}
		if returned, _ := browserSession["transport"].(string); returned == "" {
			browserSession["transport"] = in.Transport
		} else if returned != in.Transport {
			return nil, fmt.Errorf("%w: provider %q returned transport %q for %q request", ErrUnavailable, provider.ID(), returned, in.Transport)
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
	providerID := in.Provider
	if resolvedProvider != nil {
		providerID = resolvedProvider.ID()
	}
	sess := &clientSession{
		id: id, transport: in.Transport, provider: providerID,
		agentID: in.AgentID, transcript: []TranscriptEntry{},
	}
	s.sessions[id] = sess
	s.mu.Unlock()

	out := map[string]any{"sessionId": id, "transport": in.Transport, "provider": providerID, "resumed": false}
	if browserSession != nil {
		out["browserSession"] = browserSession
	}
	return out, nil
}

func (s *ClientStore) browserProvider(id, transport string) (realtimevoice.Provider, BrowserSessionProvider, error) {
	if s.voice == nil {
		return nil, nil, fmt.Errorf("%w: no realtimevoice registry wired", ErrUnavailable)
	}
	if id != "" {
		provider, ok := s.voice.Get(id)
		if !ok {
			return nil, nil, fmt.Errorf("%w: unknown realtime voice provider %q", ErrUnavailable, id)
		}
		browserProvider, ok := provider.(BrowserSessionProvider)
		if !ok {
			return nil, nil, fmt.Errorf("%w: realtime voice provider %q does not support browser-owned sessions", ErrUnavailable, provider.ID())
		}
		if supported, advertised := browserpkg.SupportsTransport(provider, transport); advertised && !supported {
			return nil, nil, fmt.Errorf("%w: realtime voice provider %q does not support transport %q", ErrUnavailable, provider.ID(), transport)
		}
		return provider, browserProvider, nil
	}

	providers := s.voice.List()
	// Prefer configured providers that explicitly advertise the requested
	// transport, then configured legacy/plugin providers without metadata. Only
	// fall back to unconfigured providers so the resulting error names the actual
	// missing provider configuration.
	for _, requireConfigured := range []bool{true, false} {
		for _, requireAdvertised := range []bool{true, false} {
			for _, provider := range providers {
				browserProvider, ok := provider.(BrowserSessionProvider)
				if !ok || (requireConfigured && !provider.Configured()) {
					continue
				}
				supported, advertised := browserpkg.SupportsTransport(provider, transport)
				if advertised && !supported {
					continue
				}
				if requireAdvertised != advertised {
					continue
				}
				return provider, browserProvider, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("%w: no realtime voice provider supports browser transport %q", ErrUnavailable, transport)
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
