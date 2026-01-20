package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/task"
)

func TestInterruptCLI_StopsRunningSend(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping interrupt CLI test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("skipping interrupt CLI test on Windows")
	}

	binPath := buildSubtask(t)
	mockWorkerPath := mockWorkerPathForSubtask(binPath)
	root := setupParallelTestRepo(t, 1, mockWorkerPath)

	taskName := "interrupt/flow"

	// Draft task.
	draftCmd := exec.Command(binPath, "draft", taskName, "Test task description",
		"--base-branch", "main", "--title", "Interrupt test")
	draftCmd.Dir = root
	out, err := draftCmd.CombinedOutput()
	require.NoError(t, err, "draft failed: %s", out)

	// Start send in background.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	longPrompt := mockPrompt("Do something long-running") + "\n/MockRunCommand sleep 30"
	sendCmd := exec.CommandContext(ctx, binPath, "send", taskName, longPrompt)
	sendCmd.Dir = root
	require.NoError(t, sendCmd.Start())

	// Wait until state shows the task is running.
	statePath := filepath.Join(root, ".subtask", "internal", task.EscapeName(taskName), "state.json")
	var runningState task.State
	require.NoError(t, waitForState(t, statePath, func(s task.State) bool {
		runningState = s
		return s.SupervisorPID != 0 && !s.StartedAt.IsZero()
	}))
	require.NotZero(t, runningState.SupervisorPGID)

	// Interrupt the task.
	interruptCmd := exec.Command(binPath, "interrupt", taskName)
	interruptCmd.Dir = root
	out, err = interruptCmd.CombinedOutput()
	require.NoError(t, err, "interrupt failed: %s", out)
	require.Contains(t, string(out), "Sent SIGINT")

	// Send should exit with an error code due to signal handler os.Exit(1).
	done := make(chan error, 1)
	go func() { done <- sendCmd.Wait() }()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-ctx.Done():
		t.Fatalf("send did not exit after interrupt: %v", ctx.Err())
	}

	// State should be cleared and contain an interruption error.
	var cleared task.State
	require.NoError(t, waitForState(t, statePath, func(s task.State) bool {
		cleared = s
		return s.SupervisorPID == 0
	}))
	require.Zero(t, cleared.SupervisorPGID)
	require.Contains(t, strings.ToLower(cleared.LastError), "interrupted")

	// History should contain interrupt + finished events.
	historyPath := filepath.Join(root, ".subtask", "tasks", task.EscapeName(taskName), "history.jsonl")
	events := readHistoryEvents(t, historyPath)

	require.True(t, hasHistoryEvent(events, "worker.interrupt", func(data map[string]any) bool {
		return data["action"] == "requested" && data["signal"] == "SIGINT"
	}), "expected worker.interrupt requested")

	require.True(t, hasHistoryEvent(events, "worker.interrupt", func(data map[string]any) bool {
		return data["action"] == "received"
	}), "expected worker.interrupt received")

	require.True(t, hasHistoryEvent(events, "worker.finished", func(data map[string]any) bool {
		return data["outcome"] == "error" && strings.Contains(strings.ToLower(toString(data["error"])), "interrupted")
	}), "expected worker.finished error")
}

func waitForState(t *testing.T, statePath string, pred func(task.State) bool) error {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(statePath)
		if err == nil {
			var s task.State
			if json.Unmarshal(b, &s) == nil && pred(s) {
				return nil
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	return context.DeadlineExceeded
}

func readHistoryEvents(t *testing.T, historyPath string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(historyPath)
	require.NoError(t, err)

	var out []map[string]any
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev map[string]any
		if json.Unmarshal([]byte(line), &ev) == nil {
			out = append(out, ev)
		}
	}
	return out
}

func hasHistoryEvent(events []map[string]any, typ string, pred func(data map[string]any) bool) bool {
	for _, ev := range events {
		if ev["type"] != typ {
			continue
		}
		raw := ev["data"]
		b, _ := json.Marshal(raw)
		var data map[string]any
		_ = json.Unmarshal(b, &data)
		if pred(data) {
			return true
		}
	}
	return false
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}
