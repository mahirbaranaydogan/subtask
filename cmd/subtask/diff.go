package main

import (
	"fmt"
	"os"

	"github.com/zippoxer/subtask/pkg/git"
	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
	"github.com/zippoxer/subtask/pkg/task/migrate"
)

// DiffCmd implements 'subtask diff'.
type DiffCmd struct {
	Task string `arg:"" help:"Task name to diff"`
	Stat bool   `help:"Show diffstat (files changed with lines added/removed)"`
}

// Run executes the diff command.
func (c *DiffCmd) Run() error {
	if _, err := preflightProject(); err != nil {
		return err
	}

	if err := migrate.EnsureSchema(c.Task); err != nil {
		return err
	}

	t, err := task.Load(c.Task)
	if err != nil {
		return err
	}

	state, err := task.LoadState(c.Task)
	if err != nil {
		return err
	}
	tail, _ := history.Tail(c.Task)

	// Merged tasks: show the squash commit diff if available.
	if tail.TaskStatus == task.TaskStatusMerged && !git.BranchExists(".", c.Task) {
		sha := tail.LastMergedCommit
		if sha == "" {
			return fmt.Errorf("diff unavailable: task %s is merged and has no branch (missing merge commit)\n\nSend to reopen:\n  subtask send %s \"<prompt>\"", c.Task, c.Task)
		}
		if c.Stat {
			return git.RunWithStderrFilter(".", git.FilterLineEndingWarnings, "show", "--stat", "--format=", sha)
		}
		return git.RunWithStderrFilter(".", git.FilterLineEndingWarnings, "show", sha)
	}

	// Prefer diffing from the task workspace when available (includes uncommitted changes).
	if state != nil && state.Workspace != "" && dirExists(state.Workspace) {
		return c.runWorkspaceDiff(t, state)
	}

	return c.runRefDiff(t, state)
}

func (c *DiffCmd) runWorkspaceDiff(t *task.Task, state *task.State) error {
	base, err := git.ResolveDiffBase(state.Workspace, "HEAD", t.BaseBranch)
	if err != nil {
		return err
	}
	args := []string{"diff"}
	if c.Stat {
		args = append(args, "--stat")
	}
	args = append(args, base)
	return git.RunWithStderrFilter(state.Workspace, git.FilterLineEndingWarnings, args...)
}

func (c *DiffCmd) runRefDiff(t *task.Task, state *task.State) error {
	repoDir := "."

	// Task branch name == task name.
	branch := c.Task
	if !git.BranchExists(repoDir, branch) {
		tail, _ := history.Tail(c.Task)
		if tail.TaskStatus == task.TaskStatusOpen {
			return fmt.Errorf("task %s hasn't started yet (no branch)\n\nStart it first:\n  subtask send %s \"<prompt>\"", c.Task, c.Task)
		}
		return fmt.Errorf("cannot diff %s: branch no longer exists", c.Task)
	}

	base, err := git.ResolveDiffBase(repoDir, branch, t.BaseBranch)
	if err != nil {
		return err
	}
	args := []string{"diff"}
	if c.Stat {
		args = append(args, "--stat")
	}
	args = append(args, base+".."+branch)
	return git.RunWithStderrFilter(repoDir, git.FilterLineEndingWarnings, args...)
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	return st.IsDir()
}
