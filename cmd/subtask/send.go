package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/zippoxer/subtask/pkg/git"
	"github.com/zippoxer/subtask/pkg/harness"
	"github.com/zippoxer/subtask/pkg/logging"
	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
	"github.com/zippoxer/subtask/pkg/task/migrate"
	"github.com/zippoxer/subtask/pkg/workspace"
)

// SendCmd implements 'subtask send'.
type SendCmd struct {
	Task   string `arg:"" help:"Task name"`
	Prompt string `arg:"" optional:"" help:"Message to send (or use stdin)"`
	Model  string `help:"Override model for this prompt (does not persist)"`
	// Reasoning is codex-only (maps to model_reasoning_effort); not persisted.
	Reasoning string `help:"Override reasoning for this prompt (codex-only; does not persist)"`
	Quiet     bool   `short:"q" help:"Suppress non-essential output (print reply only)"`

	// Internal: injected harness for testing
	testHarness harness.Harness
}

// WithHarness returns a copy with injected harness for testing.
func (c *SendCmd) WithHarness(h harness.Harness) *SendCmd {
	c.testHarness = h
	return c
}

// Run executes the send command.
func (c *SendCmd) Run() error {
	prompt := strings.TrimSpace(c.Prompt)
	if prompt == "" {
		prompt = readStdinIfAvailable()
	}
	if prompt == "" {
		return fmt.Errorf("prompt is required\n\nProvide a prompt as argument or via stdin (heredoc/pipe)")
	}

	// Requirements: git + global config (config may be migrated on first access).
	res, err := preflightProject()
	if err != nil {
		return err
	}
	cfg := res.Config

	// Ensure schema/history exist (one-time).
	if err := migrate.EnsureSchema(c.Task); err != nil {
		return err
	}

	t, err := task.Load(c.Task)
	if err != nil {
		return fmt.Errorf("task %q not found\n\nCreate it first:\n  subtask draft %s --base-branch <branch> --title \"...\"",
			c.Task, c.Task)
	}

	if err := workspace.ValidateReasoningFlag(cfg.Harness, c.Reasoning); err != nil {
		return err
	}

	// Best-effort cleanup for stale supervisor PIDs.
	task.CleanupStaleTasks()

	// Ensure the supervisor is in its own process group so that other processes
	// (harness CLIs, etc.) can be interrupted via a single group signal.
	task.EnsureOwnProcessGroup()

	// Determine durable task state.
	tail, _ := history.Tail(c.Task)

	progress, _ := task.LoadProgress(c.Task)
	if progress == nil {
		progress = &task.Progress{}
	}

	// Create harness (needed for context session migration).
	var h harness.Harness
	if c.testHarness != nil {
		h = c.testHarness
	} else {
		model := workspace.ResolveModel(cfg, t, c.Model)
		reasoning := workspace.ResolveReasoning(cfg, t, c.Reasoning)
		h, err = harness.New(workspace.ConfigWithModelReasoning(cfg, model, reasoning))
		if err != nil {
			return err
		}
	}

	// Acquire/reuse workspace + mark running + write history start events.
	runID, err := history.NewRunID()
	if err != nil {
		return err
	}

	var runToolCalls atomic.Int64

	// Start time is stored atomically so the SIGINT handler can read it safely. We update
	// it later once the worker is about to run (excluding workspace prep time).
	var startedUnixNano atomic.Int64
	startedUnixNano.Store(time.Now().UTC().UnixNano())

	// Setup signal handling early so an interrupt during workspace prep doesn't leave a
	// stuck SupervisorPID claim.
	sigChan := make(chan os.Signal, 1)
	sigStop := make(chan struct{})
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		var sig os.Signal
		select {
		case sig = <-sigChan:
		case <-sigStop:
			return
		}
		finished := time.Now().UTC()
		started := time.Unix(0, startedUnixNano.Load()).UTC()
		durationMS := int(finished.Sub(started).Milliseconds())
		if durationMS < 0 {
			durationMS = 0
		}
		errMsg := "interrupted"

		owned := false
		_ = task.WithLock(c.Task, func() error {
			st, _ := task.LoadState(c.Task)
			if st == nil {
				return nil
			}
			if st.SupervisorPID != os.Getpid() {
				return nil
			}

			owned = true
			_ = history.AppendLocked(c.Task, history.Event{
				Type: "worker.interrupt",
				Data: mustJSON(map[string]any{
					"action":          "received",
					"run_id":          runID,
					"signal":          sig.String(),
					"supervisor_pid":  os.Getpid(),
					"supervisor_pgid": task.SelfProcessGroupID(),
				}),
				TS: finished,
			})
			_ = history.AppendLocked(c.Task, history.Event{
				Type: "worker.finished",
				Data: mustJSON(map[string]any{
					"run_id":        runID,
					"duration_ms":   durationMS,
					"outcome":       "error",
					"error":         errMsg,
					"error_message": errMsg,
					"tool_calls":    int(runToolCalls.Load()),
				}),
				TS: finished,
			})

			st.SupervisorPID = 0
			st.SupervisorPGID = 0
			st.StartedAt = time.Time{}
			st.LastError = errMsg
			return st.Save(c.Task)
		})

		if owned {
			logging.Error("harness", fmt.Sprintf("task=%s %s error: %s", c.Task, cfg.Harness, errMsg))
			logging.Info("worker", fmt.Sprintf("task=%s finished outcome=error duration=%s", c.Task, finished.Sub(started).Round(time.Second)))
		}
		os.Exit(1)
	}()
	defer func() {
		close(sigStop)
		signal.Stop(sigChan)
	}()

	wsPath, prevWorkspace, continueFrom, repoStatus, err := c.prepareWorkspaceAndState(cfg, h, t, tail, prompt, runID)
	if err != nil {
		return err
	}

	// If we continued a session and workspace changed, migrate it if supported.
	if continueFrom != "" && prevWorkspace != "" && filepath.Clean(prevWorkspace) != filepath.Clean(wsPath) {
		_ = history.Append(c.Task, history.Event{
			Type: "worker.session",
			Data: mustJSON(map[string]any{
				"action":     "migrated",
				"harness":    cfg.Harness,
				"session_id": continueFrom,
			}),
		})
		_ = h.MigrateSession(continueFrom, prevWorkspace, wsPath)
	}

	// Build prompt.
	fullPrompt := harness.BuildPrompt(t, wsPath, false, prompt, repoStatus)

	// Reset start time for the worker run (exclude workspace preparation).
	startedUnixNano.Store(time.Now().UTC().UnixNano())

	// Snapshot shared files before execution (exclude history.jsonl).
	sharedBefore := SnapshotTaskFiles(c.Task)

	c.info(fmt.Sprintf("Sending to task: %s", c.Task))
	c.info("[Waiting for worker...]")
	if !c.Quiet {
		fmt.Println()
		fmt.Println()
		c.info("Tip: Don't check or poll, you'll be notified when done.")
	}

	// runToolCalls is tracked atomically for accurate interruption accounting.

	callbacks := harness.Callbacks{
		OnToolCall: func(tm time.Time) {
			runToolCalls.Add(1)
			progress.ToolCalls++
			progress.LastActive = tm
			_ = progress.Save(c.Task)
		},
		OnSessionStart: func(sessionID string) {
			_ = task.WithLock(c.Task, func() error {
				st, _ := task.LoadState(c.Task)
				if st == nil {
					st = &task.State{}
				}
				st.SessionID = sessionID
				st.Harness = cfg.Harness
				return st.Save(c.Task)
			})
			_ = history.Append(c.Task, history.Event{
				Type: "worker.session",
				Data: mustJSON(map[string]any{
					"action":     "started",
					"harness":    cfg.Harness,
					"session_id": sessionID,
				}),
			})
		},
	}

	result, runErr := h.Run(context.Background(), wsPath, fullPrompt, continueFrom, callbacks)
	finished := time.Now().UTC()
	started := time.Unix(0, startedUnixNano.Load()).UTC()
	durationMS := int(finished.Sub(started).Milliseconds())

	reply := ""
	nextSessionID := ""
	if result != nil {
		reply = result.Reply
		nextSessionID = result.SessionID
	}

	// Defensive: treat "success with empty reply" as an error so we don't write empty
	// worker messages to history.jsonl. This can happen if a harness/CLI fails to
	// surface an error (or returns AgentReplied=false without a hard error).
	if runErr == nil && strings.TrimSpace(reply) == "" {
		errMsg := "worker produced empty reply"
		if result != nil && strings.TrimSpace(result.SessionID) != "" {
			errMsg = fmt.Sprintf("%s (session %s)", errMsg, strings.TrimSpace(result.SessionID))
		}
		if result != nil && strings.TrimSpace(result.Error) == "" {
			result.Error = errMsg
		}
		runErr = fmt.Errorf("%s", errMsg)
	}

	if runErr != nil {
		errMsg := strings.TrimSpace(runErr.Error())
		if result != nil && strings.TrimSpace(result.Error) != "" {
			errMsg = strings.TrimSpace(result.Error)
		}
		if errMsg == "" {
			errMsg = "worker failed"
		}

		// When a subprocess in the supervisor's process group receives SIGINT, some
		// harnesses surface this as an exec error ("signal: interrupt") rather than
		// triggering our process-level signal handler. Treat that case as an
		// interruption for consistent state/history semantics.
		if isLikelyInterruptedError(errMsg) {
			errMsg = "interrupted"
			_ = history.Append(c.Task, history.Event{
				Type: "worker.interrupt",
				Data: mustJSON(map[string]any{
					"action":          "received",
					"run_id":          runID,
					"signal":          "SIGINT",
					"supervisor_pid":  os.Getpid(),
					"supervisor_pgid": task.SelfProcessGroupID(),
				}),
				TS: finished,
			})
		}

		_ = history.Append(c.Task, history.Event{
			Type: "worker.finished",
			Data: mustJSON(map[string]any{
				"run_id":        runID,
				"duration_ms":   durationMS,
				"tool_calls":    int(runToolCalls.Load()),
				"outcome":       "error",
				"error":         errMsg,
				"error_message": errMsg,
			}),
			TS: finished,
		})

		// Clear running fields after history is written, before printing/returning.
		_ = task.WithLock(c.Task, func() error {
			st, _ := task.LoadState(c.Task)
			if st == nil {
				st = &task.State{}
			}
			st.SupervisorPID = 0
			st.SupervisorPGID = 0
			st.StartedAt = time.Time{}
			st.LastError = errMsg
			if nextSessionID != "" {
				st.SessionID = nextSessionID
			}
			return st.Save(c.Task)
		})

		logging.Error("harness", fmt.Sprintf("task=%s %s error: %s", c.Task, cfg.Harness, errMsg))
		logging.Info("worker", fmt.Sprintf("task=%s finished outcome=error duration=%s", c.Task, finished.Sub(started).Round(time.Second)))
		return runErr
	}

	// Snapshot shared files after execution and find changes.
	sharedAfter := SnapshotTaskFiles(c.Task)
	changedFiles := ChangedTaskFiles(sharedBefore, sharedAfter)

	// Success: append worker message + finish event.
	_ = history.Append(c.Task, history.Event{
		Type:    "message",
		Role:    "worker",
		Content: reply,
		TS:      finished,
	})
	_ = history.Append(c.Task, history.Event{
		Type: "worker.finished",
		Data: mustJSON(map[string]any{
			"run_id":      runID,
			"duration_ms": durationMS,
			"tool_calls":  int(runToolCalls.Load()),
			"outcome":     "replied",
		}),
		TS: finished,
	})

	// Clear running fields after history is written, before printing output.
	_ = task.WithLock(c.Task, func() error {
		st, _ := task.LoadState(c.Task)
		if st == nil {
			st = &task.State{}
		}
		st.SupervisorPID = 0
		st.SupervisorPGID = 0
		st.StartedAt = time.Time{}
		st.LastError = ""
		if nextSessionID != "" {
			st.SessionID = nextSessionID
			st.Harness = cfg.Harness
		}
		return st.Save(c.Task)
	})

	logging.Info("worker", fmt.Sprintf("task=%s finished outcome=replied duration=%s", c.Task, finished.Sub(started).Round(time.Second)))

	if c.Quiet {
		if reply != "" {
			fmt.Print(reply)
			if !strings.HasSuffix(reply, "\n") {
				fmt.Println()
			}
		}
		return nil
	}

	PrintWorkerResultWithStage(c.Task, reply, int(runToolCalls.Load()), changedFiles, "")
	return nil
}

func (c *SendCmd) prepareWorkspaceAndState(cfg *workspace.Config, h harness.Harness, t *task.Task, tail history.TailInfo, prompt, runID string) (wsPath, prevWorkspace, continueFrom string, repoStatus *harness.RepoStatus, err error) {
	now := time.Now().UTC()

	var st *task.State
	if loaded, err := task.LoadState(c.Task); err == nil {
		st = loaded
	}

	// Session compatibility: don't attempt to continue a session across harnesses.
	if st != nil && strings.TrimSpace(st.SessionID) != "" {
		prevHarness := sessionHarnessForTask(c.Task, st)
		if prevHarness != "" && prevHarness != cfg.Harness {
			// Best-effort: persist inferred harness for future runs.
			if strings.TrimSpace(st.Harness) == "" {
				_ = task.WithLock(c.Task, func() error {
					locked, _ := task.LoadState(c.Task)
					if locked == nil {
						locked = &task.State{}
					}
					if strings.TrimSpace(locked.Harness) == "" {
						locked.Harness = prevHarness
						_ = locked.Save(c.Task)
					}
					return nil
				})
			}
			return "", "", "", nil, fmt.Errorf("task %q has an existing session from harness %q, but this project is configured for %q\n\n"+
				"Sessions are not compatible across harnesses.\n"+
				"Tip: clear the session by deleting state.json, or use a new task.",
				c.Task, prevHarness, cfg.Harness)
		}
	}

	// Hard guard: don't allow two concurrent sends on the same machine.
	if st != nil && st.SupervisorPID != 0 && !st.IsStale() {
		return "", "", "", nil, fmt.Errorf("task %s is still working\n\nYou'll be notified when done, then you can send more context.\nTo correct a worker going the wrong direction:\n  subtask interrupt %s && subtask send %s \"...\"", c.Task, c.Task, c.Task)
	}

	// Test-only: deterministic barrier to coordinate concurrent send attempts.
	if err := maybeWaitSendBarrier(); err != nil {
		return "", "", "", nil, err
	}

	claimedPID := os.Getpid()
	claimed := false
	defer func() {
		if !claimed || err == nil {
			return
		}
		errMsg := strings.TrimSpace(err.Error())
		if errMsg == "" {
			errMsg = "send failed"
		}
		_ = task.WithLock(c.Task, func() error {
			locked, _ := task.LoadState(c.Task)
			if locked == nil {
				return nil
			}
			if locked.SupervisorPID != claimedPID {
				return nil
			}
			locked.SupervisorPID = 0
			locked.SupervisorPGID = 0
			locked.StartedAt = time.Time{}
			locked.LastError = errMsg
			return locked.Save(c.Task)
		})
	}()

	// Claim the task early (before git worktree operations) to prevent a race where two sends
	// concurrently try to check out the same branch in different worktrees.
	if err := task.WithLock(c.Task, func() error {
		locked, _ := task.LoadState(c.Task)
		if locked == nil {
			locked = &task.State{}
		}
		if locked.SupervisorPID != 0 && !locked.IsStale() {
			return fmt.Errorf("task %s is still working\n\nYou'll be notified when done, then you can send more context.\nTo correct a worker going the wrong direction:\n  subtask interrupt %s && subtask send %s \"...\"", c.Task, c.Task, c.Task)
		}
		locked.SupervisorPID = claimedPID
		locked.SupervisorPGID = task.SelfProcessGroupID()
		locked.StartedAt = now
		locked.LastError = ""
		locked.Harness = cfg.Harness
		return locked.Save(c.Task)
	}); err != nil {
		return "", "", "", nil, err
	}
	claimed = true

	// Reuse workspace when available.
	if st != nil && st.Workspace != "" {
		if info, err := os.Stat(st.Workspace); err == nil && info.IsDir() {
			wsPath = st.Workspace
			c.info(fmt.Sprintf("Using existing workspace: %s", abbreviatePath(wsPath)))
		}
	}

	// Acquire new workspace if needed.
	if wsPath == "" {
		pool := workspace.NewPool()
		acq, err := pool.Acquire()
		if err != nil {
			return "", "", "", nil, err
		}
		wsPath = acq.Entry.Path
		defer acq.Release()
		c.info(fmt.Sprintf("Assigned workspace: %s", abbreviatePath(wsPath)))

		// Ensure task branch exists (open tasks reuse branch; merged tasks reopen from base).
		branchExists := git.BranchExists(wsPath, t.Name)
		switch tail.TaskStatus {
		case task.TaskStatusMerged:
			branchExists = false
		}

		if branchExists {
			if err := git.Checkout(wsPath, t.Name); err != nil {
				return "", "", "", nil, fmt.Errorf("failed to checkout branch %q: %w", t.Name, err)
			}
		} else {
			baseRef := ""
			if tail.TaskStatus != task.TaskStatusMerged {
				baseRef = strings.TrimSpace(tail.BaseCommit)
			}
			if err := git.SetupBranch(wsPath, t.Name, t.BaseBranch, baseRef); err != nil {
				// If the recorded base commit is missing (e.g., rewritten history), fall back to base branch HEAD.
				if baseRef != "" {
					if err2 := git.SetupBranch(wsPath, t.Name, t.BaseBranch, ""); err2 == nil {
						baseRef = ""
					} else {
						return "", "", "", nil, fmt.Errorf("git setup failed: %w", err)
					}
				} else {
					return "", "", "", nil, fmt.Errorf("git setup failed: %w", err)
				}
			}
		}

		ensureTaskSymlink(wsPath, c.Task)

		// If reopening from merged/closed, record a task.opened event.
		if tail.TaskStatus != task.TaskStatusOpen {
			baseCommit, _ := git.Output(wsPath, "rev-parse", "HEAD")
			data := mustJSON(map[string]any{
				"reason":      "reopen",
				"from":        string(tail.TaskStatus),
				"branch":      c.Task,
				"base_branch": t.BaseBranch,
				"base_commit": baseCommit,
			})
			_ = history.Append(c.Task, history.Event{Type: "task.opened", Data: data})
		}
	}

	// Compute repoStatus warning (best-effort).
	if t.BaseBranch != "" {
		// Local-first: compare against the local base branch only.
		target := t.BaseBranch
		if git.BranchExists(wsPath, target) {
			conflicts, err := git.MergeConflictFiles(wsPath, target, "HEAD")
			if err == nil && len(conflicts) > 0 {
				repoStatus = &harness.RepoStatus{ConflictFiles: conflicts}
			}
		}
	}

	// Follow-up: seed session from a previous task/session (before marking running).
	var followUpSeed *followUpSeed
	if (st == nil || strings.TrimSpace(st.SessionID) == "") && strings.TrimSpace(t.FollowUp) != "" {
		seed, err := resolveFollowUpSeed(cfg.Harness, t.FollowUp)
		if err != nil {
			return "", "", "", nil, err
		}
		followUpSeed = seed
	}

	// Set running state and append start events.
	err = task.WithLock(c.Task, func() error {
		locked, _ := task.LoadState(c.Task)
		if locked == nil {
			locked = &task.State{}
		}
		if locked.SupervisorPID != 0 && !locked.IsStale() && locked.SupervisorPID != os.Getpid() {
			return fmt.Errorf("task %s is still working\n\nYou'll be notified when done, then you can send more context.\nTo correct a worker going the wrong direction:\n  subtask interrupt %s && subtask send %s \"...\"", c.Task, c.Task, c.Task)
		}
		prevWorkspace = locked.Workspace

		locked.Workspace = wsPath
		locked.SupervisorPID = os.Getpid()
		locked.SupervisorPGID = task.SelfProcessGroupID()
		locked.StartedAt = now
		locked.LastError = ""
		locked.Harness = cfg.Harness

		// If this is a follow-up task, duplicate (or continue) the prior session once
		// and persist it before running.
		if strings.TrimSpace(locked.SessionID) == "" && followUpSeed != nil && strings.TrimSpace(followUpSeed.FromSessionID) != "" {
			newSessionID := ""
			if cfg.Harness != "opencode" {
				dup, err := h.DuplicateSession(followUpSeed.FromSessionID, followUpSeed.FromWorkspace, wsPath)
				if err == nil && strings.TrimSpace(dup) != "" {
					newSessionID = strings.TrimSpace(dup)
				} else if cfg.Harness == "claude" {
					// Claude sessions are stored under a cwd-specific project directory; without
					// duplication we can't safely resume from a follow-up task.
					if err == nil {
						err = fmt.Errorf("duplicate session returned empty session ID")
					}
					return fmt.Errorf("failed to duplicate follow-up session from %q: %w\n\nTip: run without --follow-up to start a fresh session.", t.FollowUp, err)
				}
			}
			if strings.TrimSpace(newSessionID) == "" {
				// Fallback: continue the original session (may modify the original conversation).
				newSessionID = strings.TrimSpace(followUpSeed.FromSessionID)
			}

			locked.SessionID = newSessionID
			locked.Harness = cfg.Harness
			_ = history.AppendLocked(c.Task, history.Event{
				Type: "worker.session",
				Data: mustJSON(map[string]any{
					"action":       "follow_up",
					"harness":      cfg.Harness,
					"session_id":   newSessionID,
					"from_task":    t.FollowUp,
					"from_session": followUpSeed.FromSessionID,
				}),
				TS: now,
			})
		}
		if err := locked.Save(c.Task); err != nil {
			return err
		}

		// Persist the lead message + run start.
		_ = history.AppendLocked(c.Task, history.Event{
			Type:    "message",
			Role:    "lead",
			Content: prompt,
			TS:      now,
		})
		_ = history.AppendLocked(c.Task, history.Event{
			Type: "worker.started",
			Data: mustJSON(map[string]any{
				"run_id":       runID,
				"prompt_bytes": len([]byte(prompt)),
			}),
			TS: now,
		})
		logging.Info("worker", fmt.Sprintf("task=%s started run=%s", c.Task, runID))

		continueFrom = strings.TrimSpace(locked.SessionID)
		return nil
	})
	if err != nil {
		return "", "", "", nil, err
	}

	return wsPath, prevWorkspace, continueFrom, repoStatus, nil
}

const (
	testSendBarrierDirEnv       = "SUBTASK_TEST_SEND_BARRIER_DIR"
	testSendBarrierNEnv         = "SUBTASK_TEST_SEND_BARRIER_N"
	testSendBarrierTimeoutMSEnv = "SUBTASK_TEST_SEND_BARRIER_TIMEOUT_MS"
)

func maybeWaitSendBarrier() error {
	dir := strings.TrimSpace(os.Getenv(testSendBarrierDirEnv))
	if dir == "" {
		return nil
	}

	n := 2
	if s := strings.TrimSpace(os.Getenv(testSendBarrierNEnv)); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			n = v
		}
	}
	timeout := 5 * time.Second
	if s := strings.TrimSpace(os.Getenv(testSendBarrierTimeoutMSEnv)); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			timeout = time.Duration(v) * time.Millisecond
		}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// Signal arrival.
	p := filepath.Join(dir, fmt.Sprintf("%d", os.Getpid()))
	if f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err == nil {
		_, _ = f.WriteString("ok\n")
		_ = f.Close()
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ents, err := os.ReadDir(dir)
		if err == nil && len(ents) >= n {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("send barrier timed out waiting for %d participants (%s)", n, dir)
}

func (c *SendCmd) info(msg string) {
	if c.Quiet {
		return
	}
	printInfo(msg)
}

// readStdinIfAvailable reads from stdin only if data is piped (non-blocking).
func readStdinIfAvailable() string {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return ""
	}
	if (fi.Mode() & os.ModeCharDevice) != 0 {
		return ""
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func isLikelyInterruptedError(msg string) bool {
	msg = strings.ToLower(strings.TrimSpace(msg))
	return strings.Contains(msg, "signal: interrupt") || strings.Contains(msg, "interrupted")
}
