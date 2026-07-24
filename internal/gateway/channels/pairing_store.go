package channels

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const PairingRequestTTL = 7 * 24 * time.Hour

// PairingRequest records an actual inbound sender rejected by a pairing-policy
// account. It contains no credentials, challenge codes, or message content.
type PairingRequest struct {
	RequestID   string `json:"request_id"`
	Channel     string `json:"channel"`
	AccountID   string `json:"account_id"`
	SenderID    string `json:"sender_id"`
	CreatedAtMS int64  `json:"created_at_ms"`
	LastSeenMS  int64  `json:"last_seen_at_ms"`
	ExpiresAtMS int64  `json:"expires_at_ms"`
}

type PairingTerminal struct {
	RequestID    string `json:"request_id"`
	Resolution   string `json:"resolution"`
	ResolvedAtMS int64  `json:"resolved_at_ms"`
}

type pairingStoreDocument struct {
	Version   int               `json:"version"`
	Requests  []PairingRequest  `json:"requests"`
	Terminals []PairingTerminal `json:"terminals,omitempty"`
}

// PairingStore is an atomically persisted pending-sender catalog.
type PairingStore struct {
	mu        sync.Mutex
	path      string
	requests  map[string]PairingRequest
	terminals map[string]PairingTerminal
}

func NewPairingStore(path string) (*PairingStore, error) {
	s := &PairingStore{path: strings.TrimSpace(path), requests: map[string]PairingRequest{}, terminals: map[string]PairingTerminal{}}
	if s.path == "" {
		return s, nil
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read channel pairing store: %w", err)
	}
	var doc pairingStoreDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode channel pairing store: %w", err)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("unsupported channel pairing store version %d", doc.Version)
	}
	now := time.Now().UnixMilli()
	for _, req := range doc.Requests {
		if req.RequestID != "" && req.ExpiresAtMS > now {
			s.requests[req.RequestID] = req
		}
	}
	for _, terminal := range doc.Terminals {
		if terminal.RequestID != "" && terminal.ResolvedAtMS+PairingRequestTTL.Milliseconds() > now {
			s.terminals[terminal.RequestID] = terminal
		}
	}
	return s, nil
}

func pairingRequestID(channel, accountID, senderID string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(channel)) + "\x00" + strings.TrimSpace(accountID) + "\x00" + strings.TrimSpace(senderID)))
	return "pairing-" + hex.EncodeToString(sum[:12])
}

// UpsertObserved records a real rejected inbound sender and refreshes its TTL.
func (s *PairingStore) UpsertObserved(channel, accountID, senderID string, now time.Time) (PairingRequest, error) {
	req, _, err := s.UpsertObservedAt(channel, accountID, senderID, now, now)
	return req, err
}

// UpsertObservedAt records an observation with its source timestamp. A durable
// approval/dismissal tombstone prevents an older callback from resurrecting a
// request after the operator has resolved it.
func (s *PairingStore) UpsertObservedAt(channel, accountID, senderID string, observedAt, now time.Time) (PairingRequest, bool, error) {
	channel = normalizeChannelProvider(channel)
	accountID = strings.TrimSpace(accountID)
	senderID = strings.TrimSpace(senderID)
	if channel == "" || accountID == "" || senderID == "" {
		return PairingRequest{}, false, fmt.Errorf("channel, account_id, and sender_id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := pairingRequestID(channel, accountID, senderID)
	if terminal, ok := s.terminals[id]; ok && observedAt.UnixMilli() <= terminal.ResolvedAtMS {
		return PairingRequest{}, false, nil
	}
	req := s.requests[id]
	created := req.RequestID == ""
	if created {
		req = PairingRequest{RequestID: id, Channel: channel, AccountID: accountID, SenderID: senderID, CreatedAtMS: now.UnixMilli()}
	}
	req.LastSeenMS = now.UnixMilli()
	req.ExpiresAtMS = now.Add(PairingRequestTTL).UnixMilli()
	next := clonePairingRequests(s.requests)
	nextTerminals := clonePairingTerminals(s.terminals)
	delete(nextTerminals, id)
	next[id] = req
	if err := s.persistLocked(next, nextTerminals); err != nil {
		return PairingRequest{}, false, err
	}
	s.requests = next
	s.terminals = nextTerminals
	return req, created, nil
}

func (s *PairingStore) List(channel, accountID string, now time.Time) ([]PairingRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clonePairingRequests(s.requests)
	nextTerminals := clonePairingTerminals(s.terminals)
	changed := false
	for id, req := range next {
		if req.ExpiresAtMS <= now.UnixMilli() {
			delete(next, id)
			changed = true
		}
	}
	for id, terminal := range nextTerminals {
		if terminal.ResolvedAtMS+PairingRequestTTL.Milliseconds() <= now.UnixMilli() {
			delete(nextTerminals, id)
			changed = true
		}
	}
	if changed {
		if err := s.persistLocked(next, nextTerminals); err != nil {
			return nil, err
		}
		s.requests = next
		s.terminals = nextTerminals
	}
	channel = normalizeChannelProvider(channel)
	accountID = strings.TrimSpace(accountID)
	out := make([]PairingRequest, 0, len(s.requests))
	for _, req := range s.requests {
		if channel != "" && req.Channel != channel {
			continue
		}
		if accountID != "" && req.AccountID != accountID {
			continue
		}
		out = append(out, req)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Channel != out[j].Channel {
			return out[i].Channel < out[j].Channel
		}
		if out[i].AccountID != out[j].AccountID {
			return out[i].AccountID < out[j].AccountID
		}
		if out[i].CreatedAtMS != out[j].CreatedAtMS {
			return out[i].CreatedAtMS < out[j].CreatedAtMS
		}
		return out[i].RequestID < out[j].RequestID
	})
	return out, nil
}

// Approve commits durable access before removing the pending request.
func (s *PairingStore) Approve(requestID string, commit func(PairingRequest) error) (PairingRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req, ok := s.requests[strings.TrimSpace(requestID)]
	if !ok || req.ExpiresAtMS <= time.Now().UnixMilli() {
		return PairingRequest{}, fmt.Errorf("pending DM access request not found")
	}
	if commit == nil {
		return PairingRequest{}, fmt.Errorf("pairing approval commit is required")
	}
	if err := commit(req); err != nil {
		return PairingRequest{}, err
	}
	next := clonePairingRequests(s.requests)
	nextTerminals := clonePairingTerminals(s.terminals)
	delete(next, req.RequestID)
	nextTerminals[req.RequestID] = PairingTerminal{RequestID: req.RequestID, Resolution: "approved", ResolvedAtMS: time.Now().UnixMilli()}
	if err := s.persistLocked(next, nextTerminals); err != nil {
		return PairingRequest{}, err
	}
	s.requests = next
	s.terminals = nextTerminals
	return req, nil
}

func (s *PairingStore) Get(requestID string) (PairingRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req, ok := s.requests[strings.TrimSpace(requestID)]
	if !ok || req.ExpiresAtMS <= time.Now().UnixMilli() {
		return PairingRequest{}, false
	}
	return req, true
}

func (s *PairingStore) Dismiss(requestID string) (PairingRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req, ok := s.requests[strings.TrimSpace(requestID)]
	if !ok {
		return PairingRequest{}, fmt.Errorf("pending DM access request not found")
	}
	next := clonePairingRequests(s.requests)
	nextTerminals := clonePairingTerminals(s.terminals)
	delete(next, req.RequestID)
	nextTerminals[req.RequestID] = PairingTerminal{RequestID: req.RequestID, Resolution: "dismissed", ResolvedAtMS: time.Now().UnixMilli()}
	if err := s.persistLocked(next, nextTerminals); err != nil {
		return PairingRequest{}, err
	}
	s.requests = next
	s.terminals = nextTerminals
	return req, nil
}

func clonePairingRequests(src map[string]PairingRequest) map[string]PairingRequest {
	out := make(map[string]PairingRequest, len(src))
	for id, req := range src {
		out[id] = req
	}
	return out
}

func clonePairingTerminals(src map[string]PairingTerminal) map[string]PairingTerminal {
	out := make(map[string]PairingTerminal, len(src))
	for id, terminal := range src {
		out[id] = terminal
	}
	return out
}

func (s *PairingStore) persistLocked(requests map[string]PairingRequest, terminals map[string]PairingTerminal) error {
	if s.path == "" {
		return nil
	}
	doc := pairingStoreDocument{Version: 1, Requests: make([]PairingRequest, 0, len(requests)), Terminals: make([]PairingTerminal, 0, len(terminals))}
	for _, req := range requests {
		doc.Requests = append(doc.Requests, req)
	}
	for _, terminal := range terminals {
		doc.Terminals = append(doc.Terminals, terminal)
	}
	sort.Slice(doc.Requests, func(i, j int) bool { return doc.Requests[i].RequestID < doc.Requests[j].RequestID })
	sort.Slice(doc.Terminals, func(i, j int) bool { return doc.Terminals[i].RequestID < doc.Terminals[j].RequestID })
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode channel pairing store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create channel pairing store directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".channel-pairing-*.tmp")
	if err != nil {
		return fmt.Errorf("create channel pairing temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace channel pairing store: %w", err)
	}
	return nil
}
