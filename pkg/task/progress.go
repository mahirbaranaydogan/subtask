package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Progress is frequently-updated, informational task metadata.
// It is intentionally separated from State to avoid clobbering state transitions.
type Progress struct {
	ToolCalls  int       `json:"tool_calls,omitempty"`
	LastActive time.Time `json:"last_activity,omitempty"`
}

func progressPath(taskName string) string {
	return filepath.Join(InternalDir(), EscapeName(taskName), "progress.json")
}

// Save writes progress to .subtask/internal/<task>/progress.json.
func (p *Progress) Save(taskName string) error {
	path := progressPath(taskName)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}

	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}

	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	return os.Rename(tmpPath, path)
}

// LoadProgress reads progress from .subtask/internal/<task>/progress.json.
func LoadProgress(taskName string) (*Progress, error) {
	path := progressPath(taskName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var p Progress
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
