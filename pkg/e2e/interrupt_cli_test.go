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
	t.Setenv("SUBTASK_DIR", t.TempDir())
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
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)

	longPrompt := mockPrompt("Do something long-running") + "\n/MockRunCommand sleep 30"
	sendCmd := exec.CommandContext(ctx, binPath, "send", taskName, longPrompt)
	sendCmd.Dir = root
	sendOutPath := filepath.Join(t.TempDir(), "send.out")
	sendOutFile, err := os.Create(sendOutPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sendOutFile.Close() })
	sendCmd.Stdout = sendOutFile
	sendCmd.Stderr = sendOutFile
	require.NoError(t, sendCmd.Start())

	escaped := task.EscapeName(taskName)
	statePathCandidates := []string{
		filepath.Join(task.ProjectsDir(), task.EscapePath(root), "internal", escaped, "state.json"),
		filepath.Join(root, ".subtask", "internal", escaped, "state.json"),
	}

	// Interrupt the task.
	deadline := time.Now().Add(10 * time.Second)
	var interruptOut []byte
	for time.Now().Before(deadline) {
		interruptCmd := exec.Command(binPath, "interrupt", taskName)
		interruptCmd.Dir = root
		out, err := interruptCmd.CombinedOutput()
		if err == nil && strings.Contains(string(out), "Sent SIGINT") {
			interruptOut = out
			break
		}
		if strings.Contains(string(out), "is not working") {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		t.Fatalf("interrupt failed unexpectedly: %v\n%s\n\nsend output:\n%s\n\nstate debug:\n%s", err, out, mustReadFile(t, sendOutPath), debugStateFiles(t, taskName, root, statePathCandidates))
	}
	require.NotEmpty(t, interruptOut, "interrupt never succeeded\n\nsend output:\n%s\n\nstate debug:\n%s", mustReadFile(t, sendOutPath), debugStateFiles(t, taskName, root, statePathCandidates))

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
	require.NoError(t, waitForAnyState(t, taskName, statePathCandidates, func(s task.State) bool {
		cleared = s
		return s.SupervisorPID == 0
	}), "send output:\n%s\n\nstate debug:\n%s", mustReadFile(t, sendOutPath), debugStateFiles(t, taskName, root, statePathCandidates))
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

func waitForAnyState(t *testing.T, taskName string, statePaths []string, pred func(task.State) bool) error {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	escaped := task.EscapeName(taskName)
	for time.Now().Before(deadline) {
		candidates := append([]string{}, statePaths...)
		if matches, _ := filepath.Glob(filepath.Join(task.ProjectsDir(), "*", "internal", escaped, "state.json")); len(matches) > 0 {
			candidates = append(candidates, matches...)
		}
		for _, statePath := range candidates {
			b, err := os.ReadFile(statePath)
			if err == nil {
				var s task.State
				if json.Unmarshal(b, &s) == nil && pred(s) {
					return nil
				}
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

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

func debugStateFiles(t *testing.T, taskName, root string, candidates []string) string {
	t.Helper()
	var b strings.Builder

	b.WriteString("candidates:\n")
	for _, p := range candidates {
		b.WriteString("  - ")
		b.WriteString(p)
		b.WriteString("\n")
	}

	escaped := task.EscapeName(taskName)
	glob := filepath.Join(task.ProjectsDir(), "*", "internal", escaped, "state.json")
	matches, _ := filepath.Glob(glob)
	b.WriteString("glob:\n  ")
	b.WriteString(glob)
	b.WriteString("\n")
	for _, p := range matches {
		b.WriteString("  - ")
		b.WriteString(p)
		b.WriteString("\n")
	}

	b.WriteString("repo-local:\n")
	repoLocal := filepath.Join(root, ".subtask", "internal", escaped, "state.json")
	b.WriteString("  - ")
	b.WriteString(repoLocal)
	b.WriteString("\n")

	seen := map[string]bool{}
	for _, p := range append(append([]string{}, candidates...), append(matches, repoLocal)...) {
		if seen[p] {
			continue
		}
		seen[p] = true
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		b.WriteString("\n--- ")
		b.WriteString(p)
		b.WriteString(" ---\n")
		b.Write(data)
		b.WriteString("\n")
	}
	return b.String()
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
