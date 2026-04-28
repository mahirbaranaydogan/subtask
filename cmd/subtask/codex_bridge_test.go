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
	require.Contains(t, prompt, "Do not merge automatically")
}

func TestCodexBridgeWatchOnce_InvokesCodexExecResume(t *testing.T) {
	env := testutil.NewTestEnv(t, 0)
	taskName := "feature/fake-codex"
	createBridgeFinishedTask(t, env, taskName, "plan", "run-fake")
	require.NoError(t, (&CodexBridgeBindCmd{Lead: "lead-a", Session: "session-a", Task: taskName}).Run())

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
	require.Contains(t, log, "-C")
	require.Contains(t, log, env.RootDir)
	require.Contains(t, log, "resume")
	require.Contains(t, log, "session-a")
	require.Contains(t, log, "Task: feature/fake-codex")
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
