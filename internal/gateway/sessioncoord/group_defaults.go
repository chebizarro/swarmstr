package sessioncoord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"metiq/internal/store/state"
)

const groupDefaultsCatalogName = "session-group-defaults-v1"

// GroupDefault is the durable launch-default projection for a session group.
type GroupDefault struct {
	Name     string  `json:"name"`
	CWD      *string `json:"cwd,omitempty"`
	Worktree bool    `json:"worktree"`
}

// GroupDefaults returns one default row for every configured session group.
func (s *Service) GroupDefaults(ctx context.Context) ([]GroupDefault, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("session group service unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	groups, err := s.listGroupsLocked(ctx)
	if err != nil {
		return nil, err
	}
	explicit, err := s.groupDefaultsLocked(ctx)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]GroupDefault, len(explicit))
	for _, row := range explicit {
		byName[row.Name] = row
	}
	out := make([]GroupDefault, 0, len(groups))
	for _, name := range groups {
		row, ok := byName[name]
		if !ok {
			row = GroupDefault{Name: name}
		}
		out = append(out, row)
	}
	return out, nil
}

// UpdateGroupDefaults durably replaces the launch defaults for one known group.
func (s *Service) UpdateGroupDefaults(ctx context.Context, name string, cwd *string, worktree bool) ([]GroupDefault, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("session group service unavailable")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if cwd != nil {
		trimmed := strings.TrimSpace(*cwd)
		cwd = &trimmed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	groups, err := s.listGroupsLocked(ctx)
	if err != nil {
		return nil, err
	}
	known := false
	for _, group := range groups {
		if group == name {
			known = true
			break
		}
	}
	if !known {
		return nil, fmt.Errorf("unknown session group: %s", name)
	}
	rows, err := s.groupDefaultsLocked(ctx)
	if err != nil {
		return nil, err
	}
	next := make([]GroupDefault, 0, len(rows)+1)
	for _, row := range rows {
		if row.Name != name {
			next = append(next, row)
		}
	}
	next = append(next, GroupDefault{Name: name, CWD: cwd, Worktree: worktree})
	if err := s.putGroupDefaultsLocked(ctx, next); err != nil {
		return nil, err
	}
	s.emitLocked("sessions.changed", map[string]any{"reason": "groups"})

	byName := make(map[string]GroupDefault, len(next))
	for _, row := range next {
		byName[row.Name] = row
	}
	out := make([]GroupDefault, 0, len(groups))
	for _, group := range groups {
		row, ok := byName[group]
		if !ok {
			row = GroupDefault{Name: group}
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *Service) groupDefaultsLocked(ctx context.Context) ([]GroupDefault, error) {
	doc, err := s.repo.GetList(ctx, groupDefaultsCatalogName)
	if errors.Is(err, state.ErrNotFound) {
		return []GroupDefault{}, nil
	}
	if err != nil {
		return nil, err
	}
	rows := make([]GroupDefault, 0, len(doc.Items))
	for _, item := range doc.Items {
		var row GroupDefault
		if err := json.Unmarshal([]byte(item), &row); err != nil {
			return nil, fmt.Errorf("decode session group defaults: %w", err)
		}
		row.Name = strings.TrimSpace(row.Name)
		if row.Name != "" {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (s *Service) putGroupDefaultsLocked(ctx context.Context, rows []GroupDefault) error {
	items := make([]string, 0, len(rows))
	for _, row := range rows {
		raw, err := json.Marshal(row)
		if err != nil {
			return err
		}
		items = append(items, string(raw))
	}
	_, err := s.repo.PutList(ctx, groupDefaultsCatalogName, state.ListDoc{Version: 1, Name: groupDefaultsCatalogName, Items: items})
	return err
}

func (s *Service) rewriteGroupDefaultsLocked(ctx context.Context, from, to string) error {
	rows, err := s.groupDefaultsLocked(ctx)
	if err != nil {
		return err
	}
	changed := false
	next := rows[:0]
	for _, row := range rows {
		if row.Name != from {
			next = append(next, row)
			continue
		}
		changed = true
		if to != "" {
			row.Name = to
			next = append(next, row)
		}
	}
	if !changed {
		return nil
	}
	return s.putGroupDefaultsLocked(ctx, next)
}
