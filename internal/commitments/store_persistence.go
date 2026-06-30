package commitments

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type storeFile struct {
	Version     int          `json:"version"`
	Commitments []Commitment `json:"commitments"`
}

func (s *Store) load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var f storeFile
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	if s.commitments == nil {
		s.commitments = map[string]Commitment{}
	}
	for _, c := range f.Commitments {
		s.commitments[c.ID] = c
	}
	return nil
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	items := make([]Commitment, 0, len(s.commitments))
	for _, c := range s.commitments {
		items = append(items, c)
	}
	data, err := json.MarshalIndent(storeFile{Version: 1, Commitments: items}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}
