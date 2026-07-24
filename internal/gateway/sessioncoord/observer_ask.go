package sessioncoord

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Observer ask bounds (OpenClaw session-observer parity).
const (
	observerAskTimeout       = 10 * time.Second
	observerAskMaxQuestion   = 400
	observerAskMaxAnswer     = 600
	observerAskMaxConcurrent = 6
	observerAskRateWindow    = time.Minute
	observerAskMaxPerWindow  = 12
	observerAskMaxPerConn    = 4
)

// ObserverAskProvider answers one bounded operator question about a running
// session using only that session's observation context. Installed by the
// daemon; absent means the observer is unavailable.
type ObserverAskProvider func(ctx context.Context, sessionKey, question string) (string, error)

// Observer ask error taxonomy.
var (
	ErrObserverUnavailable = errors.New("session observer is unavailable")
	ErrObserverBusy        = errors.New("the session observer is answering another question")
	ErrObserverRateLimited = errors.New("the session observer has reached its question limit; try again shortly")
)

type askAdmission struct {
	connectionID string
	admittedAtMS int64
}

type observerAskState struct {
	inflight   map[string]struct{}
	admissions []askAdmission
}

// SetObserverAskProvider installs the observer ask provider contract.
func (s *Service) SetObserverAskProvider(provider ObserverAskProvider) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.observerAskProvider = provider
	s.mu.Unlock()
}

// ObserverAsk answers one bounded question about a session. Admission is
// bounded before any provider work starts: one in-flight ask per session, at
// most six concurrent asks, and rolling per-minute global and per-connection
// windows. Draft sessions admit only managers.
func (s *Service) ObserverAsk(ctx context.Context, key, question, connectionID string, actor Actor) (string, error) {
	key, question = strings.TrimSpace(key), strings.TrimSpace(question)
	connectionID = strings.TrimSpace(connectionID)
	if s == nil || s.repo == nil {
		return "", ErrObserverUnavailable
	}
	if key == "" || question == "" {
		return "", fmt.Errorf("sessionKey and question are required")
	}
	if len(question) > observerAskMaxQuestion {
		return "", fmt.Errorf("question must not exceed %d characters", observerAskMaxQuestion)
	}
	if connectionID == "" {
		return "", fmt.Errorf("sessions.observer.ask requires a gateway WS connection")
	}

	s.mu.Lock()
	provider := s.observerAskProvider
	if provider == nil {
		s.mu.Unlock()
		return "", ErrObserverUnavailable
	}
	if err := s.requireSessionLocked(ctx, key); err != nil {
		s.mu.Unlock()
		return "", err
	}
	sharing, err := s.sharingDocLocked(ctx, key)
	if err != nil {
		s.mu.Unlock()
		return "", err
	}
	role := s.resolveRoleLocked(ctx, key, sharing, actor)
	if normalizeVisibility(sharing.Visibility) == VisibilityDraft && !canManageSharing(role) {
		s.mu.Unlock()
		return "", fmt.Errorf("%w: session is draft for this connection", ErrForbidden)
	}
	if s.observerAsk.inflight == nil {
		s.observerAsk.inflight = map[string]struct{}{}
	}
	if _, busy := s.observerAsk.inflight[key]; busy {
		s.mu.Unlock()
		return "", ErrObserverBusy
	}
	nowMS := s.now().UnixMilli()
	cutoff := nowMS - observerAskRateWindow.Milliseconds()
	kept := s.observerAsk.admissions[:0]
	perConnection := 0
	for _, admission := range s.observerAsk.admissions {
		if admission.admittedAtMS < cutoff {
			continue
		}
		kept = append(kept, admission)
		if admission.connectionID == connectionID {
			perConnection++
		}
	}
	s.observerAsk.admissions = kept
	if len(s.observerAsk.inflight) >= observerAskMaxConcurrent ||
		len(s.observerAsk.admissions) >= observerAskMaxPerWindow ||
		perConnection >= observerAskMaxPerConn {
		s.mu.Unlock()
		return "", ErrObserverRateLimited
	}
	s.observerAsk.admissions = append(s.observerAsk.admissions, askAdmission{connectionID: connectionID, admittedAtMS: nowMS})
	s.observerAsk.inflight[key] = struct{}{}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.observerAsk.inflight, key)
		s.mu.Unlock()
	}()

	askCtx, cancel := context.WithTimeout(ctx, observerAskTimeout)
	defer cancel()
	answer, err := provider(askCtx, key, question)
	if err != nil {
		return "", fmt.Errorf("the session observer could not answer right now: %w", err)
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", fmt.Errorf("the session observer returned an empty answer")
	}
	if runes := []rune(answer); len(runes) > observerAskMaxAnswer {
		answer = string(runes[:observerAskMaxAnswer])
	}
	return answer, nil
}
