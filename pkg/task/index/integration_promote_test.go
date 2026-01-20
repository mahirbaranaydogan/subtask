package index_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
	"github.com/zippoxer/subtask/pkg/testutil"

	taskindex "github.com/zippoxer/subtask/pkg/task/index"
)

func TestIndex_IntegrationPromoteClosed_NoCommits_DoesNotMarkMerged(t *testing.T) {
	env := testutil.NewTestEnv(t, 1)
	ctx := context.Background()

	name := "idx/promote-no-commits"
	env.CreateTask(name, "No commits", "main", "desc")

	baseCommit := gitOut(t, env.RootDir, "rev-parse", "main")
	env.CreateTaskHistory(name, []history.Event{
		{TS: time.Now().UTC().Add(-2 * time.Hour), Type: "task.opened", Data: mustJSON(map[string]any{
			"reason":      "draft",
			"base_branch": "main",
			"base_commit": baseCommit,
		})},
		{TS: time.Now().UTC().Add(-1 * time.Hour), Type: "task.closed", Data: mustJSON(map[string]any{"reason": "abandon"})},
	})

	// Create the task branch at the recorded base commit, but do not add commits.
	ws := env.Workspaces[0]
	gitOut(t, ws, "checkout", "-b", name, baseCommit)

	// Advance main so the ancestor check would otherwise succeed.
	gitOut(t, env.RootDir, "checkout", "main")
	gitOut(t, env.RootDir, "commit", "--allow-empty", "-m", "advance main")

	idx, err := taskindex.OpenDefault()
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.Refresh(ctx, taskindex.RefreshPolicy{
		Git: taskindex.GitPolicy{Mode: taskindex.GitOpenOnly, IncludeIntegration: true},
	}))

	tail, err := history.Tail(name)
	require.NoError(t, err)
	require.Equal(t, task.TaskStatusClosed, tail.TaskStatus)

	// Ensure no detected merge event was appended.
	events, err := history.Read(name, history.ReadOptions{})
	require.NoError(t, err)
	for _, ev := range events {
		require.NotEqual(t, "task.merged", strings.TrimSpace(ev.Type))
	}
}

func TestIndex_IntegrationPromoteClosed_UnfinishedRun_DoesNotMarkMerged(t *testing.T) {
	env := testutil.NewTestEnv(t, 1)
	ctx := context.Background()

	name := "idx/promote-unfinished-run"
	env.CreateTask(name, "Unfinished run", "main", "desc")

	baseCommit := gitOut(t, env.RootDir, "rev-parse", "main")
	env.CreateTaskHistory(name, []history.Event{
		{TS: time.Now().UTC().Add(-3 * time.Hour), Type: "task.opened", Data: mustJSON(map[string]any{
			"reason":      "draft",
			"base_branch": "main",
			"base_commit": baseCommit,
		})},
		{TS: time.Now().UTC().Add(-2 * time.Hour), Type: "worker.started", Data: mustJSON(map[string]any{"run_id": "r1"})},
		{TS: time.Now().UTC().Add(-1 * time.Hour), Type: "task.closed", Data: mustJSON(map[string]any{"reason": "abandon"})},
	})

	// Create a branch with a commit, then integrate it into main, so the task would be
	// detected as integrated, but should not be promoted due to unfinished run.
	ws := env.Workspaces[0]
	gitOut(t, ws, "checkout", "-b", name, baseCommit)
	gitOut(t, ws, "commit", "--allow-empty", "-m", "work")
	gitOut(t, env.RootDir, "checkout", "main")
	gitOut(t, env.RootDir, "merge", "--no-ff", name, "-m", "Merge "+name)

	idx, err := taskindex.OpenDefault()
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })

	require.NoError(t, idx.Refresh(ctx, taskindex.RefreshPolicy{
		Git: taskindex.GitPolicy{Mode: taskindex.GitOpenOnly, IncludeIntegration: true},
	}))

	tail, err := history.Tail(name)
	require.NoError(t, err)
	require.Equal(t, task.TaskStatusClosed, tail.TaskStatus)
	require.False(t, tail.RunningSince.IsZero(), "expected Tail to treat the run as still running")

	events, err := history.Read(name, history.ReadOptions{})
	require.NoError(t, err)
	for _, ev := range events {
		require.NotEqual(t, "task.merged", strings.TrimSpace(ev.Type))
	}
}
