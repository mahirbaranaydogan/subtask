package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
	"github.com/zippoxer/subtask/pkg/testutil"
	"github.com/zippoxer/subtask/pkg/workflow"
)

func TestMergeRequiresFinalWorkflowStage(t *testing.T) {
	env := testutil.NewTestEnv(t, 0)
	taskName := "merge/stage-guard"
	env.CreateTask(taskName, "Stage guard", "main", "desc")
	env.CreateTaskHistory(taskName, []history.Event{
		{Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})},
		{Type: "stage.changed", Data: mustJSON(map[string]any{"from": "", "to": "doing"})},
	})
	require.NoError(t, workflow.CopyToTask("default", taskName))

	err := requireReadyToMerge(taskName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not ready to merge")
	require.Contains(t, err.Error(), "required: ready")
}

func TestMergeReadyStagePasses(t *testing.T) {
	env := testutil.NewTestEnv(t, 0)
	taskName := "merge/ready"
	env.CreateTask(taskName, "Ready", "main", "desc")
	env.CreateTaskHistory(taskName, []history.Event{
		{Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})},
		{Type: "stage.changed", Data: mustJSON(map[string]any{"from": "", "to": "ready"})},
	})
	require.NoError(t, workflow.CopyToTask("default", taskName))

	require.NoError(t, requireReadyToMerge(taskName))
}

func TestMergeAlreadyClosedSkipsStageGuard(t *testing.T) {
	env := testutil.NewTestEnv(t, 0)
	taskName := "merge/already-merged"
	env.CreateTask(taskName, "Already merged", "main", "desc")
	env.CreateTaskHistory(taskName, []history.Event{
		{Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})},
		{Type: "stage.changed", Data: mustJSON(map[string]any{"from": "", "to": "doing"})},
		{Type: "task.merged", Data: mustJSON(map[string]any{"commit": "abc", "into": "main"})},
	})
	require.NoError(t, workflow.CopyToTask("default", taskName))

	tail, err := history.Tail(taskName)
	require.NoError(t, err)
	require.Equal(t, task.TaskStatusMerged, tail.TaskStatus)
	require.NoError(t, requireReadyToMerge(taskName))
}
