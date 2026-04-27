package main

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
	"github.com/zippoxer/subtask/pkg/testutil"
)

func TestNotifyOnce_DeduplicatesLatestWorkerFinished(t *testing.T) {
	env := testutil.NewTestEnv(t, 0)
	require.NoError(t, os.Setenv("SUBTASK_NOTIFY", "0"))
	t.Cleanup(func() { _ = os.Unsetenv("SUBTASK_NOTIFY") })

	taskName := "notify/replied"
	env.CreateTask(taskName, "Notify replied", "main", "desc")
	env.CreateTaskHistory(taskName, []history.Event{
		{
			Type: "task.opened",
			TS:   time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
			Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main", "base_commit": gitCmdOutput(t, env.RootDir, "rev-parse", "HEAD")}),
		},
		{
			Type: "worker.finished",
			TS:   time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC),
			Data: mustJSON(map[string]any{"run_id": "run-1", "duration_ms": 1000, "tool_calls": 3, "outcome": "replied"}),
		},
	})

	count, err := notifyOnce()
	require.NoError(t, err)
	require.Equal(t, 1, count)

	count, err = notifyOnce()
	require.NoError(t, err)
	require.Equal(t, 0, count)

	state, err := loadNotifyState()
	require.NoError(t, err)
	require.Equal(t, "run-1", state[taskName])
	require.FileExists(t, task.InternalDir()+"/notifications.json")
}
