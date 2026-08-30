package commitments

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const storeFileVersion = 2

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
	if f.Version < 0 || f.Version > storeFileVersion {
		return fmt.Errorf("unsupported commitments store version %d", f.Version)
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
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	data, err := json.MarshalIndent(storeFile{Version: storeFileVersion, Commitments: items}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".commitments-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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
	return os.Rename(tmpName, s.path)
}
