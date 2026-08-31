package checkpoint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ArtifactStore owns local legacy snapshot artifacts used when transcript graph
// nodes are unavailable. It refuses cleanup outside its configured root.
type ArtifactStore struct {
	root string
}

func NewArtifactStore(root string) (*ArtifactStore, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("checkpoint artifact root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &ArtifactStore{root: filepath.Clean(abs)}, nil
}

func (s *ArtifactStore) Write(id string, snapshot any) (ArtifactRef, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, `/\\`) {
		return ArtifactRef{}, fmt.Errorf("invalid checkpoint artifact id")
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return ArtifactRef{}, err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return ArtifactRef{}, err
	}
	path := filepath.Join(s.root, "checkpoint-"+id+".json")
	tmp, err := os.CreateTemp(s.root, ".checkpoint-*.tmp")
	if err != nil {
		return ArtifactRef{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return ArtifactRef{}, err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return ArtifactRef{}, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return ArtifactRef{}, err
	}
	if err := tmp.Close(); err != nil {
		return ArtifactRef{}, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return ArtifactRef{}, err
	}
	if err := syncDirectory(s.root); err != nil {
		return ArtifactRef{}, err
	}
	return ArtifactRef{ID: id, Path: path, Bytes: int64(len(raw)), Version: 1}, nil
}

func (s *ArtifactStore) Read(ref ArtifactRef, out any) error {
	path, err := s.ownedPath(ref)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (s *ArtifactStore) Delete(ref ArtifactRef) error {
	path, err := s.ownedPath(ref)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return syncDirectory(s.root)
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *ArtifactStore) ownedPath(ref ArtifactRef) (string, error) {
	if strings.TrimSpace(ref.ID) == "" || strings.ContainsAny(ref.ID, `/\\`) {
		return "", fmt.Errorf("invalid checkpoint artifact id")
	}
	expected := filepath.Join(s.root, "checkpoint-"+ref.ID+".json")
	path := expected
	if strings.TrimSpace(ref.Path) != "" {
		abs, err := filepath.Abs(ref.Path)
		if err != nil {
			return "", err
		}
		path = filepath.Clean(abs)
	}
	rel, err := filepath.Rel(s.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || path != expected {
		return "", fmt.Errorf("checkpoint artifact path is outside owned root")
	}
	return path, nil
}
