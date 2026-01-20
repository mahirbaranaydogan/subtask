package index_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	taskindex "github.com/zippoxer/subtask/pkg/task/index"

	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
	"github.com/zippoxer/subtask/pkg/testutil"
)

func TestIndex_RefreshAndList_AllOrder(t *testing.T) {
	env := testutil.NewTestEnv(t, 0)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	// a/open (idle)
	env.CreateTask("a/draft", "Draft task", "main", "Draft description")
	env.CreateTaskHistory("a/draft", []history.Event{
		{TS: now.Add(-10 * time.Minute), Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})},
		{TS: now.Add(-10 * time.Minute), Type: "stage.changed", Data: mustJSON(map[string]any{"from": "", "to": "implement"})},
	})

	// b/open + running
	env.CreateTask("b/working", "Working task", "main", "Working description")
	env.CreateTaskState("b/working", &task.State{
		SupervisorPID: os.Getpid(),
		StartedAt:     now.Add(-2 * time.Minute),
	})
	env.CreateTaskHistory("b/working", []history.Event{
		{TS: now.Add(-9 * time.Minute), Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})},
		{TS: now.Add(-9 * time.Minute), Type: "stage.changed", Data: mustJSON(map[string]any{"from": "", "to": "implement"})},
	})
	env.CreateTaskProgress("b/working", &task.Progress{ToolCalls: 3, LastActive: now.Add(-30 * time.Second)})
	require.NoError(t, os.WriteFile(filepath.Join(task.Dir("b/working"), "PROGRESS.json"), []byte(`[
  {"step":"Investigate","done":false},
  {"step":"Fix","done":false}
]`), 0o644))

	// c/open + replied
	env.CreateTask("c/replied", "Replied task", "main", "Replied description")
	env.CreateTaskHistory("c/replied", []history.Event{
		{TS: now.Add(-2 * time.Hour), Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})},
		{TS: now.Add(-90 * time.Minute), Type: "stage.changed", Data: mustJSON(map[string]any{"from": "implement", "to": "review"})},
		{TS: now.Add(-90 * time.Minute), Type: "worker.finished", Data: mustJSON(map[string]any{"run_id": "r1", "duration_ms": 0, "tool_calls": 0, "outcome": "replied"})},
	})
	env.CreateTaskProgress("c/replied", &task.Progress{ToolCalls: 12, LastActive: now.Add(-2 * time.Hour)})

	idx, err := taskindex.OpenDefault()
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.Refresh(ctx, taskindex.RefreshPolicy{}))

	items, err := idx.ListAll(ctx)
	require.NoError(t, err)

	// Recency ordering: most recently updated first
	// b/working: -9min, a/draft: -10min, c/replied: -90min
	require.GreaterOrEqual(t, len(items), 3)
	require.Equal(t, "b/working", items[0].Name)
	require.Equal(t, "a/draft", items[1].Name)
	require.Equal(t, "c/replied", items[2].Name)

	// Progress summary derived from PROGRESS.json
	require.Equal(t, 0, items[0].ProgressDone)
	require.Equal(t, 2, items[0].ProgressTotal)
}

func TestIndex_Invalidation_TASKmd(t *testing.T) {
	_ = testutil.NewTestEnv(t, 0)
	ctx := context.Background()

	name := "inv/taskmd"
	require.NoError(t, (&task.Task{
		Name:        name,
		Title:       "Old title",
		BaseBranch:  "main",
		Description: "desc",
		Schema:      1,
	}).Save())
	require.NoError(t, history.WriteAll(name, []history.Event{{Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})}}))

	idx, err := taskindex.OpenDefault()
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.Refresh(ctx, taskindex.RefreshPolicy{}))

	rec, ok, err := idx.Get(ctx, name)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "Old title", rec.Task.Title)

	// Edit TASK.md title.
	loaded, err := task.Load(name)
	require.NoError(t, err)
	loaded.Title = "New title"
	require.NoError(t, loaded.Save())

	require.NoError(t, idx.Refresh(ctx, taskindex.RefreshPolicy{}))

	rec, ok, err = idx.Get(ctx, name)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "New title", rec.Task.Title)
}

func TestIndex_NewAndDeletedTasks(t *testing.T) {
	env := testutil.NewTestEnv(t, 0)
	ctx := context.Background()

	env.CreateTask("a/one", "One", "main", "desc")
	env.CreateTaskHistory("a/one", []history.Event{{Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})}})

	idx, err := taskindex.OpenDefault()
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.Refresh(ctx, taskindex.RefreshPolicy{}))

	all, err := idx.ListAll(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)

	// Add a task.
	env.CreateTask("b/two", "Two", "main", "desc")
	env.CreateTaskHistory("b/two", []history.Event{{Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})}})

	require.NoError(t, idx.Refresh(ctx, taskindex.RefreshPolicy{}))

	all, err = idx.ListAll(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)

	// Delete a task folder.
	require.NoError(t, os.RemoveAll(task.Dir("a/one")))
	require.NoError(t, idx.Refresh(ctx, taskindex.RefreshPolicy{}))

	all, err = idx.ListAll(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, "b/two", all[0].Name)
}

func TestIndex_StaleWorkingTask_RefreshesWithoutFileChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("processAlive uses unix semantics")
	}
	env := testutil.NewTestEnv(t, 0)
	ctx := context.Background()

	// Start a short-lived process; it is alive during the initial refresh and dead afterwards.
	cmd := exec.Command("sleep", "0.1")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid

	env.CreateTask("stale/working", "Stale task", "main", "desc")
	env.CreateTaskState("stale/working", &task.State{
		SupervisorPID: pid,
	})
	env.CreateTaskHistory("stale/working", []history.Event{{Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})}})

	idx, err := taskindex.OpenDefault()
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.Refresh(ctx, taskindex.RefreshPolicy{}))

	// Wait for the pid to exit; state.json is unchanged, but the process is now dead.
	require.NoError(t, cmd.Wait())

	require.NoError(t, idx.Refresh(ctx, taskindex.RefreshPolicy{}))

	st, err := task.LoadState("stale/working")
	require.NoError(t, err)
	require.NotNil(t, st)
	require.Equal(t, 0, st.SupervisorPID)
	require.NotEmpty(t, st.LastError)
}

func TestIndex_CorruptDB_Rebuilds(t *testing.T) {
	_ = testutil.NewTestEnv(t, 0)
	ctx := context.Background()

	name := "corrupt/db"
	require.NoError(t, (&task.Task{
		Name:        name,
		Title:       "Task",
		BaseBranch:  "main",
		Description: "desc",
		Schema:      1,
	}).Save())
	require.NoError(t, history.WriteAll(name, []history.Event{{Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})}}))

	// Write a corrupt "db".
	require.NoError(t, os.WriteFile(task.IndexPath(), []byte("not a sqlite db"), 0o644))

	idx, err := taskindex.OpenDefault()
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.Refresh(ctx, taskindex.RefreshPolicy{}))

	_, ok, err := idx.Get(ctx, name)
	require.NoError(t, err)
	require.True(t, ok)

	// Ensure the corrupt file was moved out of the way.
	matches, err := filepath.Glob(task.IndexPath() + ".corrupt-*")
	require.NoError(t, err)
	require.NotEmpty(t, matches)
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
