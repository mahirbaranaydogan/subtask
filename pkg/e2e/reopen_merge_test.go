package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
)

func TestMerge_ReopenMergedTask_MergeAgain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping merge/reopen e2e in short mode")
	}

	binPath := buildSubtask(t)
	mockWorkerPath := mockWorkerPathForSubtask(binPath)
	root := setupParallelTestRepo(t, 2, mockWorkerPath)

	taskName := "e2e/reopen-merge"

	// Draft + initial send to allocate workspace and create branch.
	cmd := exec.Command(binPath, "draft", taskName, "Test task description", "--base-branch", "main", "--title", "Reopen merge test")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "draft failed: %s", out)

	cmd = exec.Command(binPath, "send", taskName, mockPrompt("start"))
	cmd.Dir = root
	out, err = cmd.CombinedOutput()
	require.NoError(t, err, "send failed: %s", out)

	st1, err := loadStateFromDir(root, taskName)
	require.NoError(t, err)
	require.NotEmpty(t, st1.Workspace)

	// Make a commit in workspace then merge.
	ws1 := st1.Workspace
	require.NoError(t, os.WriteFile(filepath.Join(ws1, "one.txt"), []byte("one\n"), 0o644))
	run(t, ws1, "git", "add", "one.txt")
	run(t, ws1, "git", "commit", "-m", "Add one")

	cmd = exec.Command(binPath, "stage", taskName, "ready")
	cmd.Dir = root
	out, err = cmd.CombinedOutput()
	require.NoError(t, err, "stage ready failed: %s", out)

	cmd = exec.Command(binPath, "merge", taskName, "-m", "Merge one")
	cmd.Dir = root
	out, err = cmd.CombinedOutput()
	require.NoError(t, err, "merge failed: %s", out)

	historyPath := filepath.Join(root, ".subtask", "tasks", task.EscapeName(taskName), "history.jsonl")
	tail1, err := history.TailPath(historyPath)
	require.NoError(t, err)
	require.Equal(t, task.TaskStatusMerged, tail1.TaskStatus)
	require.NotEmpty(t, tail1.LastMergedCommit)

	// Sending to a merged task reopens from main with a fresh branch.
	cmd = exec.Command(binPath, "send", taskName, mockPrompt("reopen"))
	cmd.Dir = root
	out, err = cmd.CombinedOutput()
	require.NoError(t, err, "send reopen failed: %s", out)

	st2, err := loadStateFromDir(root, taskName)
	require.NoError(t, err)
	require.NotEmpty(t, st2.Workspace)

	// Confirm branch exists and has no commits ahead of main.
	branch := strings.TrimSpace(string(mustCmdOutput(t, "git", "-C", st2.Workspace, "rev-parse", "--abbrev-ref", "HEAD")))
	require.Equal(t, taskName, branch)
	ahead := strings.TrimSpace(string(mustCmdOutput(t, "git", "-C", st2.Workspace, "rev-list", "--count", "main..HEAD")))
	require.Equal(t, "0", ahead)

	// Add a new commit and merge again.
	require.NoError(t, os.WriteFile(filepath.Join(st2.Workspace, "two.txt"), []byte("two\n"), 0o644))
	run(t, st2.Workspace, "git", "add", "two.txt")
	run(t, st2.Workspace, "git", "commit", "-m", "Add two")

	cmd = exec.Command(binPath, "stage", taskName, "ready")
	cmd.Dir = root
	out, err = cmd.CombinedOutput()
	require.NoError(t, err, "stage ready before second merge failed: %s", out)

	cmd = exec.Command(binPath, "merge", taskName, "-m", "Merge two")
	cmd.Dir = root
	out, err = cmd.CombinedOutput()
	require.NoError(t, err, "second merge failed: %s", out)

	tail2, err := history.TailPath(historyPath)
	require.NoError(t, err)
	require.Equal(t, task.TaskStatusMerged, tail2.TaskStatus)
	require.NotEmpty(t, tail2.LastMergedCommit)
	require.NotEqual(t, tail1.LastMergedCommit, tail2.LastMergedCommit)

	mergedCount := countHistoryEventsOfType(t, historyPath, "task.merged")
	require.Equal(t, 2, mergedCount)
}

func mustCmdOutput(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s %v failed: %s", name, args, out)
	return out
}

func countHistoryEventsOfType(t *testing.T, path, typ string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	count := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type == typ {
			count++
		}
	}
	return count
}
