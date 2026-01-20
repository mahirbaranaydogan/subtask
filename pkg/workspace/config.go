package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/zippoxer/subtask/pkg/subtaskerr"
	"github.com/zippoxer/subtask/pkg/task"
)

const DefaultMaxWorkspaces = 20

// Config is the project configuration (.subtask/config.json).
type Config struct {
	Harness       string         `json:"harness"`
	MaxWorkspaces int            `json:"max_workspaces"`
	Options       map[string]any `json:"options,omitempty"`
}

// Entry defines a workspace.
type Entry struct {
	Name string // e.g., "workspace-1"
	Path string // e.g., "~/.subtask/workspaces/-Users-foo-code-project--1"
	ID   int    // e.g., 1
}

// LoadConfig loads the effective config (global defaults + optional project overrides).
func LoadConfig() (*Config, error) {
	userPath := task.ConfigPath()
	user, userExists, err := loadConfigFile(userPath)
	if err != nil {
		return nil, fmt.Errorf("subtask: invalid config at %s\n\nFix it with:\n  subtask config --user", userPath)
	}

	// Best-effort project override discovery (requires git; ignored if not in git).
	var project *Config
	var projectPath string
	if root, err := task.GitRootAbs(); err == nil && strings.TrimSpace(root) != "" {
		projectPath = filepath.Join(root, ".subtask", "config.json")
		project, _, err = loadConfigFile(projectPath)
		if err != nil {
			return nil, fmt.Errorf("subtask: invalid project config at %s\n\nFix it with:\n  subtask config --project", projectPath)
		}
	}

	if !userExists || user == nil {
		return nil, subtaskerr.ErrNotConfigured
	}

	effective := mergeConfig(user, project)
	if effective.MaxWorkspaces <= 0 {
		effective.MaxWorkspaces = DefaultMaxWorkspaces
	}
	return effective, nil
}

// SaveTo writes the config to a specific path.
func (c *Config) SaveTo(path string) error {
	if c.MaxWorkspaces <= 0 {
		c.MaxWorkspaces = DefaultMaxWorkspaces
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Save writes the config to the global defaults path (~/.subtask/config.json).
func (c *Config) Save() error {
	return c.SaveTo(task.ConfigPath())
}

// ListWorkspaces discovers workspaces for the current project by globbing.
func ListWorkspaces() ([]Entry, error) {
	repoRoot := task.ProjectRoot()
	escapedPath := task.EscapePath(repoRoot)
	pattern := filepath.Join(task.WorkspacesDir(), escapedPath+"--*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}

	var entries []Entry
	for _, path := range matches {
		base := filepath.Base(path)
		// Extract ID from "...-escaped-path--N"
		if idx := strings.LastIndex(base, "--"); idx != -1 {
			idStr := base[idx+2:]
			if id, err := strconv.Atoi(idStr); err == nil {
				entries = append(entries, Entry{
					Name: fmt.Sprintf("workspace-%d", id),
					Path: path,
					ID:   id,
				})
			}
		}
	}

	// Sort by ID
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ID < entries[j].ID
	})

	return entries, nil
}

func mergeConfig(user, project *Config) *Config {
	out := &Config{
		Harness:       strings.TrimSpace(user.Harness),
		MaxWorkspaces: user.MaxWorkspaces,
		Options:       make(map[string]any),
	}
	for k, v := range user.Options {
		out.Options[k] = v
	}
	if project == nil {
		return out
	}

	if strings.TrimSpace(project.Harness) != "" {
		out.Harness = strings.TrimSpace(project.Harness)
	}
	if project.MaxWorkspaces > 0 {
		out.MaxWorkspaces = project.MaxWorkspaces
	}
	for k, v := range project.Options {
		out.Options[k] = v
	}
	return out
}

func loadConfigFile(path string) (*Config, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, true, err
	}
	if cfg.Options == nil {
		cfg.Options = make(map[string]any)
	}
	return &cfg, true, nil
}
