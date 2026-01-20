package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/zippoxer/subtask/pkg/harness"
	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/workspace"
)

// ReviewCmd implements 'subtask review'.
type ReviewCmd struct {
	// Target selection (mutually exclusive)
	Task        string `help:"Review task changes against its base branch"`
	Uncommitted bool   `help:"Review uncommitted changes (staged, unstaged, untracked)"`
	Commit      string `help:"Review changes introduced by a specific commit"`

	// Optional instructions
	Prompt string `arg:"" optional:"" help:"Additional review instructions (or use stdin)"`

	// Model/reasoning overrides
	Model     string `help:"Override model for this review"`
	Reasoning string `help:"Override reasoning effort (low, medium, high, xhigh)"`

	// Internal: injected harness for testing
	testHarness harness.Harness
}

// WithHarness returns a copy with injected harness for testing.
func (c *ReviewCmd) WithHarness(h harness.Harness) *ReviewCmd {
	c.testHarness = h
	return c
}

// Run executes the review command.
func (c *ReviewCmd) Run() error {
	// Validate mutually exclusive flags
	count := 0
	if c.Task != "" {
		count++
	}
	if c.Uncommitted {
		count++
	}
	if c.Commit != "" {
		count++
	}
	if count > 1 {
		return fmt.Errorf("--task, --uncommitted, and --commit are mutually exclusive")
	}
	if count == 0 {
		return fmt.Errorf("specify one of: --task <name>, --uncommitted, or --commit <sha>")
	}

	// Read instructions from arg or stdin
	instructions := strings.TrimSpace(c.Prompt)
	if instructions == "" {
		instructions = readStdinIfAvailable()
	}

	// Requirements: git + global config (config may be migrated on first access).
	res, err := preflightProject()
	if err != nil {
		return err
	}
	cfg := res.Config

	if err := workspace.ValidateReasoningFlag(cfg.Harness, c.Reasoning); err != nil {
		return err
	}

	// Determine working directory and target
	var cwd string
	var target harness.ReviewTarget

	switch {
	case c.Task != "":
		// Load task (for base branch)
		t, err := task.Load(c.Task)
		if err != nil {
			return fmt.Errorf("failed to load task %q: %w", c.Task, err)
		}

		// Load state (for workspace)
		state, err := task.LoadState(c.Task)
		if err != nil {
			return err
		}
		if state == nil || state.Workspace == "" {
			return fmt.Errorf("task %q has no workspace\n\nRun the task first:\n  subtask send %s \"...\"", c.Task, c.Task)
		}

		cwd = state.Workspace
		target = harness.ReviewTarget{BaseBranch: t.BaseBranch}

	case c.Uncommitted:
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
		target = harness.ReviewTarget{Uncommitted: true}

	case c.Commit != "":
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
		target = harness.ReviewTarget{Commit: c.Commit}
	}

	// Run review
	var h harness.Harness
	if c.testHarness != nil {
		h = c.testHarness
	} else {
		// Apply model/reasoning overrides (nil task since review doesn't use task-level settings)
		model := workspace.ResolveModel(cfg, nil, c.Model)
		reasoning := workspace.ResolveReasoning(cfg, nil, c.Reasoning)
		h, err = harness.New(workspace.ConfigWithModelReasoning(cfg, model, reasoning))
		if err != nil {
			return err
		}
	}

	review, err := h.Review(cwd, target, instructions)
	if err != nil {
		return err
	}

	fmt.Println(review)
	return nil
}
