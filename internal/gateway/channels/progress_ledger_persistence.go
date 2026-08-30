package channels

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const progressLedgerStateVersion = 1

type progressLedgerRecorderFile struct {
	Version int                              `json:"version"`
	Rooms   map[string][]ProgressLedgerEvent `json:"rooms"`
}

type progressLedgerSchedulerFile struct {
	Version int                                         `json:"version"`
	Rooms   map[string]progressLedgerPersistedRoomState `json:"rooms"`
}

type progressLedgerPersistedRoomState struct {
	LastRun  time.Time `json:"last_run,omitempty"`
	LastPost time.Time `json:"last_post,omitempty"`
}

// NewFileProgressLedgerRecorder restores a durable bounded review window.
func NewFileProgressLedgerRecorder(path string, maxEvents int) (*ProgressLedgerRecorder, error) {
	recorder := NewProgressLedgerRecorder(maxEvents)
	recorder.path = path
	if path == "" {
		return recorder, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return recorder, nil
		}
		return nil, err
	}
	var file progressLedgerRecorderFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if file.Version < 0 || file.Version > progressLedgerStateVersion {
		return nil, fmt.Errorf("unsupported progress-ledger recorder version %d", file.Version)
	}
	for room, events := range file.Rooms {
		if len(events) > recorder.maxEvents {
			events = events[len(events)-recorder.maxEvents:]
		}
		recorder.rooms[room] = append([]ProgressLedgerEvent(nil), events...)
	}
	return recorder, nil
}

// NewFileProgressLedgerScheduler restores durable review/post throttle state.
func NewFileProgressLedgerScheduler(path string) (*ProgressLedgerScheduler, error) {
	scheduler := &ProgressLedgerScheduler{path: path, rooms: map[string]*progressLedgerRoomState{}}
	if path == "" {
		return scheduler, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return scheduler, nil
		}
		return nil, err
	}
	var file progressLedgerSchedulerFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if file.Version < 0 || file.Version > progressLedgerStateVersion {
		return nil, fmt.Errorf("unsupported progress-ledger scheduler version %d", file.Version)
	}
	for room, state := range file.Rooms {
		scheduler.rooms[room] = &progressLedgerRoomState{lastRun: state.LastRun, lastPost: state.LastPost}
	}
	return scheduler, nil
}

func (r *ProgressLedgerRecorder) saveLocked() error {
	if r == nil || r.path == "" {
		return nil
	}
	rooms := make(map[string][]ProgressLedgerEvent, len(r.rooms))
	for room, events := range r.rooms {
		rooms[room] = append([]ProgressLedgerEvent(nil), events...)
	}
	return writeProgressLedgerJSON(r.path, progressLedgerRecorderFile{Version: progressLedgerStateVersion, Rooms: rooms})
}

func (s *ProgressLedgerScheduler) saveLocked() error {
	if s == nil || s.path == "" {
		return nil
	}
	rooms := make(map[string]progressLedgerPersistedRoomState, len(s.rooms))
	for room, state := range s.rooms {
		if state != nil {
			rooms[room] = progressLedgerPersistedRoomState{LastRun: state.lastRun, LastPost: state.lastPost}
		}
	}
	return writeProgressLedgerJSON(s.path, progressLedgerSchedulerFile{Version: progressLedgerStateVersion, Rooms: rooms})
}

func writeProgressLedgerJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".progress-ledger-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
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
	return os.Rename(tmpName, path)
}
