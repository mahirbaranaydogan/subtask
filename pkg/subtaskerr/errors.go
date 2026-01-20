package subtaskerr

import "errors"

var (
	// ErrNotConfigured is returned when ~/.subtask/config.json is missing and no automatic migration applies.
	ErrNotConfigured = errors.New("subtask: not configured — run 'subtask install' first")
	// ErrNotGitRepo is returned when a command requires git but the cwd is not inside a git repository.
	ErrNotGitRepo = errors.New("subtask: not a git repository — subtask requires git")

	// ErrNoAnchorFromWorkspace is returned when running from a worker workspace and Subtask cannot
	// determine the main repo anchor worktree.
	ErrNoAnchorFromWorkspace = errors.New("subtask: cannot determine project root from within a worker workspace")
)

