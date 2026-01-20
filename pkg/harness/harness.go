package harness

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zippoxer/subtask/pkg/git"
	"github.com/zippoxer/subtask/pkg/workspace"
)

// Result is the output from running a harness.
type Result struct {
	Reply           string
	SessionID       string
	PromptDelivered bool   // True if session started (thread.started seen)
	AgentReplied    bool   // True if agent sent a message
	Error           string // Non-empty if execution failed
}

// Callbacks for harness events.
type Callbacks struct {
	OnSessionStart func(sessionID string) // Called when session starts (thread.started)
	OnToolCall     func(time.Time)        // Called for each tool call
}

// ReviewTarget specifies what to review.
type ReviewTarget struct {
	// Exactly one of these should be set:
	Uncommitted bool   // Review staged, unstaged, and untracked changes
	BaseBranch  string // Review changes against this base branch
	Commit      string // Review changes introduced by this commit SHA
}

// Harness is the interface for worker backends.
type Harness interface {
	// Run executes the worker. Blocks until completion.
	Run(ctx context.Context, cwd, prompt, continueFrom string, cb Callbacks) (*Result, error)

	// Review runs harness-specific review command.
	// target specifies what to review (uncommitted, base branch, or commit).
	// instructions is optional additional review instructions.
	Review(cwd string, target ReviewTarget, instructions string) (string, error)

	// MigrateSession moves a session from oldCwd to newCwd when a workspace path changes.
	// Returns nil if migration succeeds or is unnecessary (Codex).
	MigrateSession(sessionID, oldCwd, newCwd string) error

	// DuplicateSession creates a new session ID that starts with the same history
	// as `sessionID`, but is usable from `newCwd`.
	//
	// The original session must remain unchanged.
	// Returns the new session ID.
	DuplicateSession(sessionID, oldCwd, newCwd string) (string, error)
}

// New creates a harness from config.
func New(cfg *workspace.Config) (Harness, error) {
	switch cfg.Harness {
	case "codex":
		return &CodexHarness{
			cli:       cliSpecFromOptions(cfg.Options, "codex"),
			Model:     getStringOpt(cfg.Options, "model"),
			Reasoning: getStringOpt(cfg.Options, "reasoning"),
		}, nil
	case "claude":
		return &ClaudeHarness{
			cli:            cliSpecFromOptions(cfg.Options, "claude"),
			Model:          getStringOpt(cfg.Options, "model"),
			PermissionMode: getStringOpt(cfg.Options, "permission_mode"),
			Tools:          getStringOpt(cfg.Options, "tools"),
		}, nil
	case "opencode":
		return &OpenCodeHarness{
			cli:     cliSpecFromOptions(cfg.Options, "opencode"),
			Model:   getStringOpt(cfg.Options, "model"),
			Variant: getStringOpt(cfg.Options, "variant"),
			Agent:   getStringOpt(cfg.Options, "agent"),
		}, nil
	case "mock":
		// External mock worker - spawns a real process for realistic e2e testing.
		return &MockCLIHarness{
			cli: cliSpecFromOptions(cfg.Options, "subtask-mock-worker"),
		}, nil
	case "builtin-mock":
		// Built-in in-process mock for fast unit tests.
		return &BuiltinMock{
			ToolCalls: getIntOpt(cfg.Options, "tool_calls", 3),
		}, nil
	default:
		return nil, fmt.Errorf("unknown harness: %q", cfg.Harness)
	}
}

// BuiltinMock is a simple mock harness for CLI testing.
type BuiltinMock struct {
	ToolCalls int
}

func (m *BuiltinMock) Run(ctx context.Context, cwd, prompt, continueFrom string, cb Callbacks) (*Result, error) {
	sessionID := fmt.Sprintf("mock-session-%d", time.Now().UnixNano())

	// Notify session start
	if cb.OnSessionStart != nil {
		cb.OnSessionStart(sessionID)
	}

	// Simulate tool calls
	for i := 0; i < m.ToolCalls; i++ {
		if cb.OnToolCall != nil {
			cb.OnToolCall(time.Now())
		}
		time.Sleep(10 * time.Millisecond) // Small delay to simulate work
	}
	return &Result{
		Reply:           "Mock response for: " + prompt[:min(50, len(prompt))],
		SessionID:       sessionID,
		PromptDelivered: true,
		AgentReplied:    true,
	}, nil
}

func (m *BuiltinMock) Review(cwd string, target ReviewTarget, instructions string) (string, error) {
	var msg string
	switch {
	case target.Uncommitted:
		msg = "Mock review of uncommitted changes: No issues found."
	case target.BaseBranch != "":
		msg = fmt.Sprintf("Mock review against %s: No issues found.", target.BaseBranch)
	case target.Commit != "":
		msg = fmt.Sprintf("Mock review of commit %s: No issues found.", target.Commit)
	default:
		msg = "Mock review: No issues found."
	}
	if instructions != "" {
		msg += " (with custom instructions)"
	}
	return msg, nil
}

func (m *BuiltinMock) MigrateSession(sessionID, oldCwd, newCwd string) error {
	return nil
}

func (m *BuiltinMock) DuplicateSession(sessionID, oldCwd, newCwd string) (string, error) {
	return fmt.Sprintf("mock-session-dup-%d", time.Now().UnixNano()), nil
}

func getIntOpt(opts map[string]any, key string, def int) int {
	if v, ok := opts[key]; ok {
		switch n := v.(type) {
		case int:
			return n
		case float64:
			return int(n)
		}
	}
	return def
}

func getStringOpt(opts map[string]any, key string) string {
	if v, ok := opts[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// buildReviewPrompt constructs a review prompt for harnesses that don't have
// built-in review target support (Claude, OpenCode).
// Mirrors the prompt format from Codex's review_prompts.rs.
func buildReviewPrompt(cwd string, target ReviewTarget, instructions string) string {
	var parts []string

	switch {
	case target.Uncommitted:
		parts = append(parts, "Review the current code changes (staged, unstaged, and untracked files) and provide prioritized findings.")

	case target.BaseBranch != "":
		// Try to get merge base for more accurate diff
		mergeBase, err := git.MergeBase(cwd, "HEAD", target.BaseBranch)
		if err == nil && mergeBase != "" {
			parts = append(parts, fmt.Sprintf(
				"Review the code changes against the base branch '%s'. "+
					"The merge base commit for this comparison is %s. "+
					"Run `git diff %s` to inspect the changes relative to %s. "+
					"Provide prioritized, actionable findings.",
				target.BaseBranch, mergeBase, mergeBase, target.BaseBranch))
		} else {
			// Fallback: let the reviewer figure out the merge base
			parts = append(parts, fmt.Sprintf(
				"Review the code changes against the base branch '%s'. "+
					"Start by finding the merge diff between the current branch and %s "+
					"(e.g., `git merge-base HEAD %s`), then run `git diff` against that SHA "+
					"to see what changes we would merge into the %s branch. "+
					"Provide prioritized, actionable findings.",
				target.BaseBranch, target.BaseBranch, target.BaseBranch, target.BaseBranch))
		}

	case target.Commit != "":
		parts = append(parts, fmt.Sprintf(
			"Review the code changes introduced by commit %s. "+
				"Run `git show %s` to inspect the changes. "+
				"Provide prioritized, actionable findings.",
			target.Commit, target.Commit))

	default:
		// Fallback to uncommitted
		parts = append(parts, "Review the current code changes (staged, unstaged, and untracked files) and provide prioritized findings.")
	}

	if instructions != "" {
		parts = append(parts, strings.TrimSpace(instructions))
	}

	return strings.Join(parts, "\n\n")
}
