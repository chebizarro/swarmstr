package nip77

import (
	"encoding/hex"
	"fmt"
	"math"
	"sync"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip77/negentropy"
	"fiatjaf.com/nostr/nip77/negentropy/storage/vector"
)

const DefaultFrameSizeLimit = 60_000

type Record struct {
	Timestamp nostr.Timestamp
	ID        nostr.ID
}

type SessionOptions struct {
	Initiator       bool
	TrackLocalOnly  bool
	TrackRemoteOnly bool
	FrameSizeLimit  int
}

type ReconcileResult struct {
	Next       []byte
	LocalOnly  []nostr.ID
	RemoteOnly []nostr.ID
	Done       bool
}

type Session struct {
	mu        sync.Mutex
	engine    *negentropy.Negentropy
	initiator bool
	started   bool
	done      bool
}

func NewSession(records []Record, options SessionOptions) (*Session, error) {
	limit := options.FrameSizeLimit
	if limit == 0 {
		limit = DefaultFrameSizeLimit
	}
	if limit < 4096 {
		return nil, fmt.Errorf("NIP-77 frame size limit must be at least 4096")
	}
	store := vector.New()
	seen := make(map[nostr.ID]struct{}, len(records))
	for _, record := range records {
		if record.Timestamp < 0 || int64(record.Timestamp) == math.MaxInt64 {
			return nil, fmt.Errorf("invalid negentropy timestamp %d", record.Timestamp)
		}
		if _, duplicate := seen[record.ID]; duplicate {
			continue
		}
		seen[record.ID] = struct{}{}
		store.Insert(record.Timestamp, record.ID)
	}
	store.Seal()
	return &Session{
		engine:    negentropy.New(store, limit, options.TrackLocalOnly, options.TrackRemoteOnly),
		initiator: options.Initiator,
	}, nil
}

func (s *Session) Start() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initiator {
		return nil, protocolErrorf("only an initiator can start reconciliation")
	}
	if s.started {
		return nil, protocolErrorf("session already started")
	}
	s.started = true
	message, err := hex.DecodeString(s.engine.Start())
	if err != nil {
		return nil, protocolErrorf("decode generated initial message: %v", err)
	}
	if err := validateMessage(message); err != nil {
		return nil, err
	}
	return message, nil
}

func (s *Session) Reconcile(message []byte) (ReconcileResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return ReconcileResult{}, protocolErrorf("session is complete")
	}
	if s.initiator && !s.started {
		return ReconcileResult{}, protocolErrorf("session has not been started")
	}
	if err := validateMessage(message); err != nil {
		return ReconcileResult{}, err
	}
	type outcome struct {
		next string
		err  error
	}
	finished := make(chan outcome, 1)
	go func() {
		next, err := s.engine.Reconcile(hex.EncodeToString(message))
		finished <- outcome{next: next, err: err}
	}()

	result := ReconcileResult{}
	localCh := s.engine.Haves
	remoteCh := s.engine.HaveNots
	for {
		select {
		case id, ok := <-localCh:
			if !ok {
				localCh = nil
			} else {
				result.LocalOnly = append(result.LocalOnly, id)
			}
		case id, ok := <-remoteCh:
			if !ok {
				remoteCh = nil
			} else {
				result.RemoteOnly = append(result.RemoteOnly, id)
			}
		case out := <-finished:
			if out.err != nil {
				return ReconcileResult{}, protocolErrorf("reconcile: %v", out.err)
			}
			drainIDs(&result.LocalOnly, localCh)
			drainIDs(&result.RemoteOnly, remoteCh)
			if out.next != "" {
				next, err := hex.DecodeString(out.next)
				if err != nil {
					return ReconcileResult{}, protocolErrorf("decode generated message: %v", err)
				}
				if err := validateMessage(next); err != nil {
					return ReconcileResult{}, err
				}
				result.Next = next
			} else if s.initiator {
				result.Done = true
				s.done = true
			}
			result.LocalOnly = uniqueIDs(result.LocalOnly)
			result.RemoteOnly = uniqueIDs(result.RemoteOnly)
			return result, nil
		}
	}
}

func drainIDs(target *[]nostr.ID, ch <-chan nostr.ID) {
	if ch == nil {
		return
	}
	for {
		select {
		case id, ok := <-ch:
			if !ok {
				return
			}
			*target = append(*target, id)
		default:
			return
		}
	}
}

func uniqueIDs(ids []nostr.ID) []nostr.ID {
	seen := make(map[nostr.ID]struct{}, len(ids))
	out := make([]nostr.ID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
