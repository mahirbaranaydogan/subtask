package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
)

func TestSendCLI_BasicFlowAndWorkingGuard(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping send CLI test in short mode")
	}
	t.Setenv("SUBTASK_DIR", t.TempDir())

	binPath := buildSubtask(t)
	mockWorkerPath := mockWorkerPathForSubtask(binPath)
	root := setupParallelTestRepo(t, 2, mockWorkerPath)

	taskName := "send/state-machine"

	// Draft task
	draftCmd := exec.Command(binPath, "draft", taskName, "Test task description",
		"--base-branch", "main", "--title", "Send test")
	draftCmd.Dir = root
	out, err := draftCmd.CombinedOutput()
	require.NoError(t, err, "draft failed: %s", out)

	// Send initial message (draft -> run logic)
	sendCmd := exec.Command(binPath, "send", taskName, mockPrompt("Do something"))
	sendCmd.Dir = root
	out, err = sendCmd.CombinedOutput()
	require.NoError(t, err, "initial send failed: %s", out)

	state, err := loadStateFromDir(root, taskName)
	require.NoError(t, err)
	assert.NotEmpty(t, state.Workspace)
	assert.Zero(t, state.SupervisorPID)
	assert.True(t, state.StartedAt.IsZero())
	assert.NotEmpty(t, state.SessionID)

	progress, err := loadProgressFromDir(root, taskName)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, progress.ToolCalls, 3)

	tail, err := history.TailPath(filepath.Join(root, ".subtask", "tasks", task.EscapeName(taskName), "history.jsonl"))
	require.NoError(t, err)
	assert.Equal(t, task.TaskStatusOpen, tail.TaskStatus)
	assert.Equal(t, "replied", tail.LastRunOutcome)
	assert.Greater(t, tail.LastRunDurationMS, 0)

	// Send follow-up message (replied -> resume logic)
	followupCmd := exec.Command(binPath, "send", taskName, mockPrompt("Continue the work"))
	followupCmd.Dir = root
	out, err = followupCmd.CombinedOutput()
	require.NoError(t, err, "follow-up send failed: %s", out)

	progress2, err := loadProgressFromDir(root, taskName)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, progress2.ToolCalls, progress.ToolCalls+3)

	// Force "working" (non-stale) and verify send errors
	statePath := filepath.Join(task.ProjectsDir(), task.EscapePath(root), "internal", task.EscapeName(taskName), "state.json")
	data, err := os.ReadFile(statePath)
	require.NoError(t, err)
	var s task.State
	require.NoError(t, json.Unmarshal(data, &s))
	s.SupervisorPID = os.Getpid()
	s.StartedAt = time.Now()
	data, err = json.MarshalIndent(&s, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(statePath, data, 0644))

	workingCmd := exec.Command(binPath, "send", taskName, "This should fail")
	workingCmd.Dir = root
	out, err = workingCmd.CombinedOutput()
	require.Error(t, err, "expected send to fail for working task")
	assert.Contains(t, string(out), "still working")
}
