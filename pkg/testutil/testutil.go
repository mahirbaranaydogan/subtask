// Package testutil provides test utilities for subtask e2e tests.
package testutil

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
	"github.com/zippoxer/subtask/pkg/task/migrate/gitredesign"
	"github.com/zippoxer/subtask/pkg/workspace"
)

// TestEnv encapsulates an isolated test environment.
type TestEnv struct {
	T          *testing.T
	RootDir    string   // Temp directory simulating project root
	Workspaces []string // Paths to workspace directories
	origCwd    string   // Original working directory
}

// NewTestEnv creates an isolated test environment with git repos.
func NewTestEnv(t *testing.T, numWorkspaces int) *TestEnv {
	t.Helper()

	origSubtaskDir, hadSubtaskDir := os.LookupEnv("SUBTASK_DIR")
	origNotify, hadNotify := os.LookupEnv("SUBTASK_NOTIFY")
	requireSetEnv(t, "SUBTASK_DIR", t.TempDir())
	requireSetEnv(t, "SUBTASK_NOTIFY", "0")
	t.Cleanup(func() {
		if hadSubtaskDir {
			_ = os.Setenv("SUBTASK_DIR", origSubtaskDir)
		} else {
			_ = os.Unsetenv("SUBTASK_DIR")
		}
		if hadNotify {
			_ = os.Setenv("SUBTASK_NOTIFY", origNotify)
		} else {
			_ = os.Unsetenv("SUBTASK_NOTIFY")
		}
	})

	// Make git commit SHAs deterministic for golden tests by pinning author/committer
	// timestamps. Tests that care about time should use history events (nowFunc), not
	// git commit metadata.
	origAuthorDate, hadAuthorDate := os.LookupEnv("GIT_AUTHOR_DATE")
	origCommitterDate, hadCommitterDate := os.LookupEnv("GIT_COMMITTER_DATE")
	requireSetEnv(t, "GIT_AUTHOR_DATE", "2026-01-01T00:00:00Z")
	requireSetEnv(t, "GIT_COMMITTER_DATE", "2026-01-01T00:00:00Z")
	t.Cleanup(func() {
		if hadAuthorDate {
			_ = os.Setenv("GIT_AUTHOR_DATE", origAuthorDate)
		} else {
			_ = os.Unsetenv("GIT_AUTHOR_DATE")
		}
		if hadCommitterDate {
			_ = os.Setenv("GIT_COMMITTER_DATE", origCommitterDate)
		} else {
			_ = os.Unsetenv("GIT_COMMITTER_DATE")
		}
	})

	// Create temp root (git repo)
	root := t.TempDir()

	// Initialize as git repo
	initGitRepo(t, root)

	// Create portable task dir (repo-local only)
	subtaskDir := filepath.Join(root, ".subtask")
	_ = os.MkdirAll(filepath.Join(subtaskDir, "tasks"), 0o755)

	// Create workspaces (git worktrees) using the standard naming convention
	// so ListWorkspaces() can discover them
	escapedPath := task.EscapePath(root)
	wsDir := task.WorkspacesDir()
	os.MkdirAll(wsDir, 0755)

	var wsPaths []string
	for i := 1; i <= numWorkspaces; i++ {
		wsPath := filepath.Join(wsDir, fmt.Sprintf("%s--%d", escapedPath, i))
		createWorktree(t, root, wsPath)
		wsPaths = append(wsPaths, wsPath)
	}

	// Create config.json (workspaces discovered from disk, not stored)
	cfg := &workspace.Config{
		Harness:       "builtin-mock",
		MaxWorkspaces: workspace.DefaultMaxWorkspaces,
		Options: map[string]any{
			"model": "gpt-5.2",
		},
	}
	cfgPath := task.ConfigPath()
	cfgData, _ := json.MarshalIndent(cfg, "", "  ")
	_ = os.MkdirAll(filepath.Dir(cfgPath), 0o755)
	_ = os.WriteFile(cfgPath, cfgData, 0o644)

	// Save original cwd and change to test root
	origCwd, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}

	env := &TestEnv{
		T:          t,
		RootDir:    root,
		Workspaces: wsPaths,
		origCwd:    origCwd,
	}

	t.Cleanup(func() {
		os.Chdir(origCwd)
	})

	return env
}

func requireSetEnv(t *testing.T, k, v string) {
	t.Helper()
	if err := os.Setenv(k, v); err != nil {
		t.Fatalf("setenv %s: %v", k, err)
	}
}

// CreateTask creates a task with TASK.md.
func (e *TestEnv) CreateTask(name, title, base, description string) *task.Task {
	e.T.Helper()
	t := &task.Task{
		Name:        name,
		Title:       title,
		BaseBranch:  base,
		Description: description,
		Schema:      gitredesign.TaskSchemaVersion,
	}
	if err := t.Save(); err != nil {
		e.T.Fatalf("failed to save task: %v", err)
	}
	return t
}

// CreateTaskHistory creates (or replaces) history.jsonl for a task.
func (e *TestEnv) CreateTaskHistory(name string, events []history.Event) {
	e.T.Helper()
	for i := range events {
		if events[i].TS.IsZero() {
			events[i].TS = time.Now().UTC()
		}
	}
	if err := history.WriteAll(name, events); err != nil {
		e.T.Fatalf("failed to write history: %v", err)
	}
}

// CreateTaskState creates a state.json for a task.
func (e *TestEnv) CreateTaskState(name string, state *task.State) {
	e.T.Helper()
	if err := state.Save(name); err != nil {
		e.T.Fatalf("failed to save state: %v", err)
	}
}

// CreateTaskProgress creates a progress.json for a task.
func (e *TestEnv) CreateTaskProgress(name string, progress *task.Progress) {
	e.T.Helper()
	if err := progress.Save(name); err != nil {
		e.T.Fatalf("failed to save progress: %v", err)
	}
}

// Config returns the loaded workspace config.
func (e *TestEnv) Config() *workspace.Config {
	e.T.Helper()
	cfg, err := workspace.LoadConfig()
	if err != nil {
		e.T.Fatalf("failed to load config: %v", err)
	}
	return cfg
}

// MakeDirty creates uncommitted changes in a workspace.
func (e *TestEnv) MakeDirty(workspaceIdx int) {
	e.T.Helper()
	if workspaceIdx >= len(e.Workspaces) {
		e.T.Fatalf("workspace index %d out of range", workspaceIdx)
	}
	path := filepath.Join(e.Workspaces[workspaceIdx], "dirty.txt")
	os.WriteFile(path, []byte("uncommitted changes"), 0644)
}

// Git helpers

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test User")

	// Keep Subtask's runtime/task folders out of test repo commits.
	ignoreFile := filepath.Join(dir, ".gitignore")
	_ = os.WriteFile(ignoreFile, []byte(".subtask/\n"), 0o644)

	// Create initial commit
	dummyFile := filepath.Join(dir, "README.md")
	os.WriteFile(dummyFile, []byte("# Test Repo\n"), 0644)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "Initial commit")

	// Rename default branch to main for consistency
	run(t, dir, "git", "branch", "-M", "main")
}

func createWorktree(t *testing.T, repoDir, worktreePath string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(worktreePath), 0755)
	run(t, repoDir, "git", "worktree", "add", "--detach", worktreePath)
}

// IsClean checks if a directory has no uncommitted changes.
func IsClean(t *testing.T, dir string) bool {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, _ := cmd.Output()
	return len(out) == 0
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %v failed: %v", name, args, err)
	}
}
