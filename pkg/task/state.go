package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zippoxer/subtask/pkg/logging"
)

// State is local, runtime-only state for a task.
//
// This file is intentionally NOT syncable: it contains machine-specific details
// (workspace path) and ephemeral execution state (PIDs).
type State struct {
	Workspace      string    `json:"workspace,omitempty"`       // absolute local path
	SessionID      string    `json:"session_id,omitempty"`      // current session
	Harness        string    `json:"harness,omitempty"`         // current session harness
	SupervisorPID  int       `json:"supervisor_pid,omitempty"`  // current run supervisor PID
	SupervisorPGID int       `json:"supervisor_pgid,omitempty"` // current run supervisor process group ID (unix)
	StartedAt      time.Time `json:"started_at,omitempty"`      // current run start (UTC)
	LastError      string    `json:"last_error,omitempty"`      // current/last run error
}

// Save writes the state to .subtask/internal/<task>/state.json.
// Uses fsync to ensure visibility to other processes (important for workspace locking).
func (s *State) Save(taskName string) error {
	path := StatePath(taskName)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	return writeBytesAtomic(path, data)
}

func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeBytesAtomic(path, data)
}

func writeBytesAtomic(path string, data []byte) error {
	// Write to temp file and rename for atomicity
	tmpPath := path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(tmpPath)
		return err
	}

	// Sync to disk before releasing any locks
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmpPath)
		return err
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	// Atomic rename
	return os.Rename(tmpPath, path)
}

// LoadState reads state from .subtask/internal/<task>/state.json.
func LoadState(taskName string) (*State, error) {
	debug := logging.DebugEnabled()
	var start time.Time
	if debug {
		start = time.Now()
	}
	path := StatePath(taskName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}

	if debug {
		d := time.Since(start)
		if d >= 5*time.Millisecond {
			logging.Debug("io", fmt.Sprintf("state.json task=%s bytes=%d (%s)", taskName, len(data), d.Round(time.Millisecond)))
		}
	}

	return &s, nil
}

// IsStale returns true if a supervisor PID is recorded but the process is dead.
func (s *State) IsStale() bool {
	if s == nil {
		return false
	}
	if s.SupervisorPID == 0 {
		return false
	}
	return !processAlive(s.SupervisorPID)
}

// CleanupStaleTasks clears stale supervisor PIDs and records an error.
// Should be called before any workspace-acquiring operation.
func CleanupStaleTasks() {
	tasks, err := List()
	if err != nil {
		return
	}
	for _, name := range tasks {
		state, err := LoadState(name)
		if err != nil || state == nil {
			continue
		}
		if !state.IsStale() {
			continue
		}

		_ = WithLock(name, func() error {
			lockedState, err := LoadState(name)
			if err != nil || lockedState == nil {
				return nil
			}
			if !lockedState.IsStale() {
				return nil
			}
			lockedState.SupervisorPID = 0
			lockedState.SupervisorPGID = 0
			lockedState.StartedAt = time.Time{}
			if strings.TrimSpace(lockedState.LastError) == "" {
				lockedState.LastError = "supervisor process died"
			}
			return lockedState.Save(name)
		})
	}
}
