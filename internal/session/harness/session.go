package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

type Session struct {
	storage *Storage
	active  string
}

func OpenSession(dir, sessionID string) (*Session, error) {
	st, err := OpenStorage(dir, sessionID)
	if err != nil {
		return nil, err
	}
	s := &Session{storage: st}
	entries := st.ReadAll()
	if len(entries) > 0 {
		s.active = entries[len(entries)-1].ID
	}
	return s, nil
}

func (s *Session) Storage() *Storage  { return s.storage }
func (s *Session) ActiveLeaf() string { return s.active }
func (s *Session) Flush() error       { return s.storage.Flush() }
func (s *Session) Close() error       { return s.storage.Close() }

func (s *Session) Append(e Entry) (string, error) {
	if e.ID == "" {
		e.ID = uuid.New().String()
	}
	if e.ParentID == nil && s.active != "" {
		p := s.active
		e.ParentID = &p
	}
	if err := s.storage.Append(e); err != nil {
		return "", err
	}
	s.active = e.ID
	return e.ID, nil
}

func (s *Session) AppendMessage(role, content string) (string, error) {
	return s.Append(Entry{Type: EntryTypeMessage, Message: &Message{Role: role, Content: content}})
}

func (s *Session) AppendToolCall(name string, args any) (string, error) {
	b, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	return s.Append(Entry{Type: EntryTypeToolCall, ToolCall: &ToolCall{Name: name, Arguments: b}})
}

func (s *Session) Branch(fromID, name string) (Branch, error) {
	fromID = strings.TrimSpace(fromID)
	if fromID == "" {
		fromID = s.active
	}
	if fromID == "" {
		return Branch{}, errors.New("cannot branch empty session")
	}
	if _, ok := s.entryMap()[fromID]; !ok {
		return Branch{}, fmt.Errorf("entry %s not found", fromID)
	}
	return Branch{LeafID: fromID, Name: name}, nil
}

func (s *Session) SwitchBranch(leafID string) error {
	if leafID == "" {
		s.active = ""
		return nil
	}
	if _, ok := s.entryMap()[leafID]; !ok {
		return fmt.Errorf("entry %s not found", leafID)
	}
	s.active = leafID
	return nil
}

func (s *Session) Branches() []Branch {
	entries := s.storage.ReadAll()
	parents := map[string]bool{}
	for _, e := range entries {
		if e.ParentID != nil {
			parents[*e.ParentID] = true
		}
	}
	branches := []Branch{}
	for _, e := range entries {
		if !parents[e.ID] {
			branches = append(branches, Branch{LeafID: e.ID})
		}
	}
	sort.Slice(branches, func(i, j int) bool { return branches[i].LeafID < branches[j].LeafID })
	return branches
}

func (s *Session) History() ([]Entry, error) { return s.HistoryFrom(s.active) }
func (s *Session) HistoryFrom(leafID string) ([]Entry, error) {
	if leafID == "" {
		return nil, nil
	}
	byID := s.entryMap()
	e, ok := byID[leafID]
	if !ok {
		return nil, fmt.Errorf("entry %s not found", leafID)
	}
	var rev []Entry
	for {
		rev = append(rev, e)
		if e.ParentID == nil {
			break
		}
		var ok bool
		e, ok = byID[*e.ParentID]
		if !ok {
			return nil, fmt.Errorf("parent %s not found", *rev[len(rev)-1].ParentID)
		}
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev, nil
}

func (s *Session) Compact(opts CompactOptions) (Entry, error) {
	hist, err := s.History()
	if err != nil {
		return Entry{}, err
	}
	if len(hist) == 0 {
		return Entry{}, errors.New("cannot compact empty session")
	}
	first := opts.FirstKeptEntryID
	if first == "" {
		first = hist[len(hist)-1].ID
	}
	kept := 0
	found := false
	for _, e := range hist {
		if e.ID == first {
			found = true
		}
		if found {
			kept++
		}
	}
	if !found {
		return Entry{}, fmt.Errorf("first kept entry %s not found", first)
	}
	span := hist[:len(hist)-kept]
	e := Entry{Type: EntryTypeCompaction, Summary: strings.TrimSpace(opts.Summary), FirstKeptEntryID: first, TokensBefore: opts.TokensBefore, TokensAfter: opts.TokensAfter, DroppedEntries: len(span), KeptEntries: kept, FileOps: ExtractFileOperations(span)}
	id, err := s.Append(e)
	if err != nil {
		return Entry{}, err
	}
	e.ID = id
	e.ParentID = ptrParent(s.active, id)
	e.Timestamp = nowISO()
	return s.entryMap()[id], nil
}

func (s *Session) Summarize(sum Summarizer, fromID string) (Entry, error) {
	if sum == nil {
		sum = DefaultSummarizer{}
	}
	hist, err := s.History()
	if err != nil {
		return Entry{}, err
	}
	res, err := sum.Summarize(hist)
	if err != nil {
		return Entry{}, err
	}
	e := Entry{Type: EntryTypeBranchSummary, FromID: fromID, Summary: res.Summary, TokensBefore: res.TokensBefore, TokensAfter: res.TokensAfter, DroppedEntries: len(hist), FileOps: ExtractFileOperations(hist)}
	id, err := s.Append(e)
	if err != nil {
		return Entry{}, err
	}
	return s.entryMap()[id], nil
}

func (s *Session) entryMap() map[string]Entry {
	m := map[string]Entry{}
	for _, e := range s.storage.ReadAll() {
		m[e.ID] = e
	}
	return m
}

func ptrParent(active, id string) *string { return nil }
