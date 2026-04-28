package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
	"github.com/zippoxer/subtask/pkg/testutil"
)

func TestCodexBridgeResolve_ExactBeatsLongestPrefix(t *testing.T) {
	state := codexBridgeState{Bindings: []codexLeadBinding{
		{Lead: "growth", SessionID: "session-growth", TaskPrefix: "growth-os/"},
		{Lead: "onboarding", SessionID: "session-onboarding", TaskPrefix: "growth-os/onboarding-"},
		{Lead: "exact", SessionID: "session-exact", Task: "growth-os/onboarding-ruleengine-full"},
	}}

	match := state.resolve("growth-os/onboarding-ruleengine-full")
	require.True(t, match.Matched)
	require.Equal(t, "exact", match.Binding.Lead)

	match = state.resolve("growth-os/onboarding-core")
	require.True(t, match.Matched)
	require.Equal(t, "onboarding", match.Binding.Lead)

	match = state.resolve("other/task")
	require.False(t, match.Matched)
}

func TestCodexBridgeBind_RejectsSameTaskDifferentLead(t *testing.T) {
	_ = testutil.NewTestEnv(t, 0)
	require.NoError(t, (&CodexBridgeBindCmd{Lead: "lead-a", Session: "session-a", Task: "feature/a"}).Run())

	err := (&CodexBridgeBindCmd{Lead: "lead-b", Session: "session-b", Task: "feature/a"}).Run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "already bound")
}

func TestCodexBridgeDeliverOnce_DeduplicatesRun(t *testing.T) {
	env := testutil.NewTestEnv(t, 0)
	taskName := "feature/replied"
	createBridgeFinishedTask(t, env, taskName, "plan", "run-1")
	require.NoError(t, (&CodexBridgeBindCmd{Lead: "lead-a", Session: "session-a", Task: taskName}).Run())

	var calls []codexBridgeResumeRequest
	restore := stubCodexBridgeResume(t, func(_ context.Context, req codexBridgeResumeRequest) error {
		calls = append(calls, req)
		return nil
	})
	defer restore()

	count, err := codexBridgeDeliverOnce(context.Background(), env.RootDir)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Len(t, calls, 1)
	require.Equal(t, "session-a", calls[0].Binding.SessionID)

	count, err = codexBridgeDeliverOnce(context.Background(), env.RootDir)
	require.NoError(t, err)
	require.Equal(t, 0, count)
	require.Len(t, calls, 1)

	deliveries, err := loadCodexBridgeDeliveries()
	require.NoError(t, err)
	require.Contains(t, deliveries.Deliveries, codexDeliveryID(taskName, "run-1"))
	require.Equal(t, codexBridgeDeliveryNotify, deliveries.Deliveries[codexDeliveryID(taskName, "run-1")].Mode)
}

func TestCodexBridgeBind_DefaultsToNotifyDelivery(t *testing.T) {
	_ = testutil.NewTestEnv(t, 0)

	cmd := CodexBridgeBindCmd{Lead: "lead-a", Session: "session-a", Task: "feature/notify"}
	binding, err := cmd.binding()
	require.NoError(t, err)
	require.Equal(t, codexBridgeDeliveryNotify, binding.deliveryMode())
}

func TestCodexBridgeBind_RejectsUnknownDelivery(t *testing.T) {
	_ = testutil.NewTestEnv(t, 0)

	err := (&CodexBridgeBindCmd{Lead: "lead-a", Session: "session-a", Task: "feature/a", Delivery: "magic"}).Run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delivery")
}

func TestCodexBridgeBind_AcceptsTerminalInjectDelivery(t *testing.T) {
	_ = testutil.NewTestEnv(t, 0)

	cmd := CodexBridgeBindCmd{
		Lead:     "lead-a",
		Session:  "session-a",
		Task:     "feature/visible",
		Delivery: codexBridgeDeliveryTerminalInject,
		TTY:      "ttys004",
	}
	binding, err := cmd.binding()
	require.NoError(t, err)
	require.Equal(t, codexBridgeDeliveryTerminalInject, binding.deliveryMode())
	require.Equal(t, "/dev/ttys004", binding.TTY)
}

func TestCodexBridgeBind_AcceptsWarpLaunchDelivery(t *testing.T) {
	_ = testutil.NewTestEnv(t, 0)

	cmd := CodexBridgeBindCmd{
		Lead:     "lead-a",
		Session:  "session-a",
		Task:     "feature/visible",
		Delivery: codexBridgeDeliveryWarpLaunch,
	}
	binding, err := cmd.binding()
	require.NoError(t, err)
	require.Equal(t, codexBridgeDeliveryWarpLaunch, binding.deliveryMode())
}

func TestCodexBridgeBindFromNow_MarksExistingRepliesDelivered(t *testing.T) {
	env := testutil.NewTestEnv(t, 0)
	taskName := "feature/from-now"
	createBridgeFinishedTask(t, env, taskName, "plan", "run-old")

	require.NoError(t, (&CodexBridgeBindCmd{
		Lead:    "lead-a",
		Session: "session-a",
		Task:    taskName,
		FromNow: true,
	}).Run())

	var calls []codexBridgeResumeRequest
	restore := stubCodexBridgeResume(t, func(_ context.Context, req codexBridgeResumeRequest) error {
		calls = append(calls, req)
		return nil
	})
	defer restore()

	count, err := codexBridgeDeliverOnce(context.Background(), env.RootDir)
	require.NoError(t, err)
	require.Equal(t, 0, count)
	require.Empty(t, calls)

	deliveries, err := loadCodexBridgeDeliveries()
	require.NoError(t, err)
	require.Contains(t, deliveries.Deliveries, codexDeliveryID(taskName, "run-old"))
}

func TestCodexBridgeDeliverOnce_RoutesMultipleLeads(t *testing.T) {
	env := testutil.NewTestEnv(t, 0)
	createBridgeFinishedTask(t, env, "backend/api", "plan", "run-backend")
	createBridgeFinishedTask(t, env, "frontend/ui", "plan", "run-frontend")
	require.NoError(t, (&CodexBridgeBindCmd{Lead: "backend-lead", Session: "session-backend", TaskPrefix: "backend/"}).Run())
	require.NoError(t, (&CodexBridgeBindCmd{Lead: "frontend-lead", Session: "session-frontend", TaskPrefix: "frontend/"}).Run())

	var sessions []string
	restore := stubCodexBridgeResume(t, func(_ context.Context, req codexBridgeResumeRequest) error {
		sessions = append(sessions, req.Binding.SessionID)
		return nil
	})
	defer restore()

	count, err := codexBridgeDeliverOnce(context.Background(), env.RootDir)
	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.ElementsMatch(t, []string{"session-backend", "session-frontend"}, sessions)
}

func TestCodexBridgeLeadLock_SerializesSameLead(t *testing.T) {
	_ = testutil.NewTestEnv(t, 0)
	started := make(chan struct{})
	release := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ok, err := withCodexLeadLock("lead-a", func() error {
			close(started)
			<-release
			return nil
		})
		require.NoError(t, err)
		require.True(t, ok)
	}()

	<-started
	ok, err := withCodexLeadLock("lead-a", func() error {
		return nil
	})
	require.NoError(t, err)
	require.False(t, ok)
	close(release)
	wg.Wait()
}

func TestCodexBridgePrompt_IncludesReviewContract(t *testing.T) {
	req := codexBridgeResumeRequest{
		RepoRoot: "/repo",
		Task:     "feature/a",
		Stage:    "plan",
		Event: finishedEvent{
			Task: "feature/a",
			Key:  "run-1",
			Data: workerFinishedData{Outcome: "replied", ToolCalls: 7},
		},
		Binding: codexLeadBinding{Lead: "lead-a", SessionID: "session-a"},
	}

	prompt := buildCodexBridgePrompt(req)
	require.Contains(t, prompt, "Task: feature/a")
	require.Contains(t, prompt, "Stage: plan")
	require.Contains(t, prompt, "Outcome: replied")
	require.Contains(t, prompt, "/repo/.subtask/tasks/feature--a/PLAN.md")
	require.Contains(t, prompt, "subtask diff --stat feature/a")
	require.Contains(t, prompt, "do not poll, sleep, watch")
	require.Contains(t, prompt, "subtask send --detach")
	require.Contains(t, prompt, "Do not merge automatically")
}

func TestCodexBridgeTerminalPrompt_IsShortVisibleLeadInstruction(t *testing.T) {
	req := codexBridgeResumeRequest{
		RepoRoot: "/repo",
		Task:     "feature/a",
		Stage:    "implement",
		Event: finishedEvent{
			Task: "feature/a",
			Key:  "run-1",
			Data: workerFinishedData{Outcome: "replied", ToolCalls: 7, DurationMS: 1200},
		},
		Binding: codexLeadBinding{Lead: "lead-a", SessionID: "session-a"},
	}

	prompt := buildCodexBridgeTerminalPrompt(req)
	require.Contains(t, prompt, "Subtask worker replied: feature/a")
	require.Contains(t, prompt, "subtask show feature/a")
	require.Contains(t, prompt, "subtask log feature/a")
	require.Contains(t, prompt, "subtask diff --stat feature/a")
	require.Contains(t, prompt, "Do not merge automatically")
	require.NotContains(t, prompt, "codex exec")
}

func TestParseCodexResumeTTY_FindsVisibleResumeAndSkipsExecResume(t *testing.T) {
	psOutput := `
  111 ??       /opt/homebrew/bin/codex exec resume session-a prompt
  222 ttys004  node /opt/homebrew/bin/codex resume session-a
  333 ttys005  /opt/homebrew/bin/codex resume session-b
`

	ttyPath, ok, err := parseCodexResumeTTY(psOutput, "session-a")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "/dev/ttys004", ttyPath)
}

func TestParseCodexResumeTTY_ReturnsFalseWithoutVisibleSession(t *testing.T) {
	psOutput := `
  111 ??       /opt/homebrew/bin/codex exec resume session-a prompt
  222 ttys005  /opt/homebrew/bin/codex resume session-b
`

	_, ok, err := parseCodexResumeTTY(psOutput, "session-a")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestCodexBridgeVisibleLaunchFiles_ReadPromptFromFile(t *testing.T) {
	env := testutil.NewTestEnv(t, 0)
	req := codexBridgeResumeRequest{
		RepoRoot: env.RootDir,
		Task:     "feature/a",
		Event: finishedEvent{
			Task: "feature/a",
			Key:  "run-1",
			Data: workerFinishedData{Outcome: "replied"},
		},
		Binding: codexLeadBinding{Lead: "lead-a", SessionID: "session-a"},
	}

	promptPath, launchPath, scriptPath, err := writeCodexBridgeVisibleLaunchFiles(req, "hello from bridge")
	require.NoError(t, err)

	prompt, err := os.ReadFile(promptPath)
	require.NoError(t, err)
	require.Equal(t, "hello from bridge\n", string(prompt))

	launch, err := os.ReadFile(launchPath)
	require.NoError(t, err)
	require.Contains(t, string(launch), "Warp Launch Configuration")
	require.Contains(t, string(launch), "codex resume 'session-a'")
	require.Contains(t, string(launch), "cat '"+promptPath+"'")
	require.Contains(t, string(launch), "cwd: "+yamlString(env.RootDir))
	require.FileExists(t, launchPath)

	script, err := os.ReadFile(scriptPath)
	require.NoError(t, err)
	require.Contains(t, string(script), "#!/bin/zsh")
	require.Contains(t, string(script), "cd '"+env.RootDir+"'")
	require.Contains(t, string(script), "codex resume 'session-a'")
	require.Contains(t, string(script), "cat '"+promptPath+"'")
	require.FileExists(t, scriptPath)
}

func TestCodexBridgeWatchOnce_InvokesCodexExecResume(t *testing.T) {
	env := testutil.NewTestEnv(t, 0)
	taskName := "feature/fake-codex"
	createBridgeFinishedTask(t, env, taskName, "plan", "run-fake")
	require.NoError(t, (&CodexBridgeBindCmd{Lead: "lead-a", Session: "session-a", Task: taskName, Delivery: codexBridgeDeliveryExecResume}).Run())

	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "codex-args.log")
	writeFakeCodex(t, binDir, logPath)
	origPath := os.Getenv("PATH")
	require.NoError(t, os.Setenv("PATH", binDir+string(os.PathListSeparator)+origPath))
	t.Cleanup(func() { _ = os.Setenv("PATH", origPath) })

	count, err := codexBridgeDeliverOnce(context.Background(), env.RootDir)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	log := string(data)
	require.Contains(t, log, "exec")
	require.Contains(t, log, "--disable apps")
	require.Contains(t, log, "shell_environment_policy.inherit=all")
	require.Contains(t, log, "-C")
	require.Contains(t, log, env.RootDir)
	require.Contains(t, log, "resume")
	require.Contains(t, log, "session-a")
	require.Contains(t, log, "Task: feature/fake-codex")
}

func TestCodexBridgeActiveResumeBlocksMergeUntilCleanup(t *testing.T) {
	testutil.NewTestEnv(t, 0)
	req := codexBridgeResumeRequest{
		Task: "feature/active",
		Event: finishedEvent{
			Task: "feature/active",
			Key:  "run-active",
			Data: workerFinishedData{Outcome: "replied"},
		},
		Binding: codexLeadBinding{Lead: "lead-a", SessionID: "session-a"},
	}

	cleanup, err := markCodexBridgeActiveResume(req, time.Minute)
	require.NoError(t, err)
	require.True(t, codexBridgeActiveResumeBlocksMerge(time.Now().UTC()))

	cleanup()
	require.False(t, codexBridgeActiveResumeBlocksMerge(time.Now().UTC()))
}

func createBridgeFinishedTask(t *testing.T, env *testutil.TestEnv, taskName, stage, runID string) {
	t.Helper()
	env.CreateTask(taskName, "Bridge task", "main", "desc")
	env.CreateTaskHistory(taskName, []history.Event{
		{
			Type: "task.opened",
			TS:   time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
			Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main", "base_commit": gitCmdOutput(t, env.RootDir, "rev-parse", "HEAD")}),
		},
		{
			Type: "stage.changed",
			TS:   time.Date(2026, 1, 1, 12, 0, 1, 0, time.UTC),
			Data: mustJSON(map[string]any{"from": "", "to": stage}),
		},
		{
			Type: "worker.finished",
			TS:   time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC),
			Data: mustJSON(map[string]any{"run_id": runID, "duration_ms": 1000, "tool_calls": 3, "outcome": "replied"}),
		},
	})
	require.FileExists(t, task.HistoryPath(taskName))
}

func stubCodexBridgeResume(t *testing.T, fn func(context.Context, codexBridgeResumeRequest) error) func() {
	t.Helper()
	orig := runCodexBridgeResume
	runCodexBridgeResume = fn
	return func() {
		runCodexBridgeResume = orig
	}
}

func writeFakeCodex(t *testing.T, dir, logPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "codex.bat")
		script := fmt.Sprintf("@echo off\r\necho %%* > %q\r\nexit /B 0\r\n", logPath)
		require.NoError(t, os.WriteFile(path, []byte(script), 0o644))
		return
	}
	path := filepath.Join(dir, "codex")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" > %s\nexit 0\n", shellQuote(logPath))
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
