package gitredesign_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
	"github.com/zippoxer/subtask/pkg/task/migrate/gitredesign"
	"github.com/zippoxer/subtask/pkg/testutil"
)

func TestEnsure_SkipsTasksAtCurrentSchemaWithoutReadingHistory(t *testing.T) {
	env := testutil.NewTestEnv(t, 0)
	repoDir := env.RootDir

	taskName := "migrate/skip"
	env.CreateTask(taskName, "Skip", "main", "desc") // schema=gitredesign.TaskSchemaVersion

	// If Ensure tried to read history.jsonl, it would hit a permission error.
	historyPath := task.HistoryPath(taskName)
	require.NoError(t, os.WriteFile(historyPath, []byte("x\n"), 0o000))
	t.Cleanup(func() { _ = os.Chmod(historyPath, 0o644) })

	require.NoError(t, gitredesign.Ensure(repoDir))
}

func TestEnsure_WritesRepoMarkerAfterSuccessfulRun(t *testing.T) {
	env := testutil.NewTestEnv(t, 0)
	repoDir := env.RootDir

	taskName := "migrate/marker"
	env.CreateTask(taskName, "Marker", "main", "desc") // schema=gitredesign.TaskSchemaVersion

	markerPath := filepath.Join(task.ProjectsDir(), task.EscapePath(repoDir), "migrations", "gitredesign-v1.done")
	_, err := os.Stat(markerPath)
	require.Error(t, err)

	require.NoError(t, gitredesign.Ensure(repoDir))
	require.FileExists(t, markerPath)
}

func TestEnsure_SkipsAllWorkWhenRepoMarkerExists(t *testing.T) {
	env := testutil.NewTestEnv(t, 0)
	repoDir := env.RootDir

	taskName := "migrate/marker-skip"
	env.CreateTask(taskName, "Marker skip", "main", "desc")

	markerPath := filepath.Join(task.ProjectsDir(), task.EscapePath(repoDir), "migrations", "gitredesign-v1.done")
	require.NoError(t, os.MkdirAll(filepath.Dir(markerPath), 0o755))
	require.NoError(t, os.WriteFile(markerPath, []byte("ok\n"), 0o644))

	// Break task.List() by making ".subtask/tasks" a file. Ensure should still succeed
	// because it should exit before scanning tasks when the marker exists.
	tasksDir := filepath.Join(repoDir, ".subtask", "tasks")
	bak := tasksDir + ".bak"
	require.NoError(t, os.Rename(tasksDir, bak))
	require.NoError(t, os.WriteFile(tasksDir, []byte("not a dir"), 0o644))
	t.Cleanup(func() {
		_ = os.Remove(tasksDir)
		_ = os.Rename(bak, tasksDir)
	})

	require.NoError(t, gitredesign.Ensure(repoDir))
}

func TestEnsure_BackfillsAndBumpsSchema_Idempotent(t *testing.T) {
	env := testutil.NewTestEnv(t, 0)
	repoDir := env.RootDir

	taskName := "migrate/backfill"
	require.NoError(t, (&task.Task{
		Name:        taskName,
		Title:       "Backfill",
		BaseBranch:  "main",
		Description: "desc",
		Schema:      1, // v0.1.1 / schema1
	}).Save())

	// Create a task branch so inferBaseCommit can use merge-base.
	gitCmd(t, repoDir, "checkout", "-b", taskName, "main")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "task.txt"), []byte("task\n"), 0o644))
	gitCmd(t, repoDir, "add", "task.txt")
	gitCmd(t, repoDir, "commit", "-m", "task commit")
	gitCmd(t, repoDir, "checkout", "main")

	// Create an arbitrary commit to use as the "merged commit" in legacy history.
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "merged.txt"), []byte("merged\n"), 0o644))
	gitCmd(t, repoDir, "add", "merged.txt")
	gitCmd(t, repoDir, "commit", "-m", "merged commit")
	mergedCommit := strings.TrimSpace(gitCmd(t, repoDir, "rev-parse", "HEAD"))

	// Legacy-ish history: opened missing base_commit, merged missing frozen stats.
	env.CreateTaskHistory(taskName, []history.Event{
		{TS: time.Now().UTC(), Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})},
		{TS: time.Now().UTC(), Type: "stage.changed", Data: mustJSON(map[string]any{"from": "", "to": "ready"})},
		{TS: time.Now().UTC(), Type: "task.merged", Data: mustJSON(map[string]any{"commit": mergedCommit, "into": "main"})},
	})

	before, err := os.ReadFile(task.HistoryPath(taskName))
	require.NoError(t, err)

	require.NoError(t, gitredesign.Ensure(repoDir))

	after, err := os.ReadFile(task.HistoryPath(taskName))
	require.NoError(t, err)
	require.NotEqual(t, string(before), string(after))

	// Schema bumped.
	loaded, err := task.Load(taskName)
	require.NoError(t, err)
	require.Equal(t, gitredesign.TaskSchemaVersion, loaded.Schema)

	// Backfilled base_commit + base_ref.
	events, err := history.Read(taskName, history.ReadOptions{})
	require.NoError(t, err)
	var openedData map[string]any
	require.NoError(t, json.Unmarshal(events[0].Data, &openedData))
	baseCommit, _ := openedData["base_commit"].(string)
	baseRef, _ := openedData["base_ref"].(string)
	require.NotEmpty(t, strings.TrimSpace(baseCommit))
	require.Equal(t, "main", strings.TrimSpace(baseRef))

	// Backfilled merged frozen stats (or recorded a frozen_error).
	var mergedData map[string]any
	require.NoError(t, json.Unmarshal(events[len(events)-1].Data, &mergedData))
	_, hasAdded := mergedData["changes_added"]
	_, hasErr := mergedData["frozen_error"]
	require.True(t, hasAdded || hasErr)

	// Idempotent: Ensure again should skip (schema already bumped) and not rewrite history.
	before2, err := os.ReadFile(task.HistoryPath(taskName))
	require.NoError(t, err)
	require.NoError(t, gitredesign.Ensure(repoDir))
	after2, err := os.ReadFile(task.HistoryPath(taskName))
	require.NoError(t, err)
	require.Equal(t, string(before2), string(after2))
}

func TestEnsure_ZeroTimestampMergedUsesCommitDate(t *testing.T) {
	env := testutil.NewTestEnv(t, 0)
	repoDir := env.RootDir

	taskName := "migrate/zero-ts-merged"
	require.NoError(t, (&task.Task{
		Name:        taskName,
		Title:       "Zero TS merged",
		BaseBranch:  "main",
		Description: "desc",
		Schema:      1,
	}).Save())

	// Create a commit to reference from the legacy merged event.
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "merged.txt"), []byte("merged\n"), 0o644))
	gitCmd(t, repoDir, "add", "merged.txt")
	gitCmd(t, repoDir, "commit", "-m", "merged commit")
	mergedCommit := strings.TrimSpace(gitCmd(t, repoDir, "rev-parse", "HEAD"))

	expected := gitCommitDate(t, repoDir, mergedCommit)

	// Write a legacy-ish history where task.merged has a zero timestamp.
	writeRawHistory(t, taskName, []history.Event{
		{TS: time.Date(2025, 12, 31, 23, 59, 0, 0, time.UTC), Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})},
		{TS: time.Time{}, Type: "task.merged", Data: mustJSON(map[string]any{"commit": mergedCommit, "into": "main"})},
	})

	require.NoError(t, gitredesign.Ensure(repoDir))

	events, err := history.Read(taskName, history.ReadOptions{})
	require.NoError(t, err)

	mergedIdx := -1
	for i := range events {
		if events[i].Type == "task.merged" {
			mergedIdx = i
		}
	}
	require.GreaterOrEqual(t, mergedIdx, 0)
	require.True(t, events[mergedIdx].TS.Equal(expected))
}

func TestEnsure_ZeroTimestampClosedUsesBranchHeadCommitDate(t *testing.T) {
	env := testutil.NewTestEnv(t, 0)
	repoDir := env.RootDir

	taskName := "migrate/zero-ts-closed"
	require.NoError(t, (&task.Task{
		Name:        taskName,
		Title:       "Zero TS closed",
		BaseBranch:  "main",
		Description: "desc",
		Schema:      1,
	}).Save())

	// Create a task branch so the migration can backfill branch_head.
	gitCmd(t, repoDir, "checkout", "-b", taskName, "main")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "task.txt"), []byte("task\n"), 0o644))
	gitCmd(t, repoDir, "add", "task.txt")
	gitCmd(t, repoDir, "commit", "-m", "task commit")
	branchHead := strings.TrimSpace(gitCmd(t, repoDir, "rev-parse", "HEAD"))
	gitCmd(t, repoDir, "checkout", "main")

	expected := gitCommitDate(t, repoDir, branchHead)

	// Write a legacy-ish history where task.closed has a zero timestamp and missing frozen stats.
	writeRawHistory(t, taskName, []history.Event{
		{TS: time.Date(2025, 12, 31, 23, 59, 0, 0, time.UTC), Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})},
		{TS: time.Time{}, Type: "task.closed", Data: mustJSON(map[string]any{"reason": "close"})},
	})

	require.NoError(t, gitredesign.Ensure(repoDir))

	events, err := history.Read(taskName, history.ReadOptions{})
	require.NoError(t, err)

	closedIdx := -1
	for i := range events {
		if events[i].Type == "task.closed" {
			closedIdx = i
		}
	}
	require.GreaterOrEqual(t, closedIdx, 0)
	require.True(t, events[closedIdx].TS.Equal(expected))
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func writeRawHistory(t *testing.T, taskName string, events []history.Event) {
	t.Helper()
	var b strings.Builder
	for _, ev := range events {
		line, err := json.Marshal(ev)
		require.NoError(t, err)
		b.Write(line)
		b.WriteByte('\n')
	}
	require.NoError(t, os.MkdirAll(task.Dir(taskName), 0o755))
	require.NoError(t, os.WriteFile(task.HistoryPath(taskName), []byte(b.String()), 0o644))
}

func gitCommitDate(t *testing.T, dir, commit string) time.Time {
	t.Helper()
	out := gitCmd(t, dir, "show", "-s", "--format=%cI", commit)
	ts, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(out))
	require.NoError(t, err)
	return ts.UTC()
}

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}
