package e2e

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
	"github.com/zippoxer/subtask/pkg/testutil"
)

func refsSnapshotHash(t *testing.T, root string) string {
	t.Helper()

	// Index is runtime-only state; it lives outside the repo in ~/.subtask/projects/<escaped-git-root>/index.db.
	dbPath := filepath.Join(task.ProjectsDir(), task.EscapePath(root), "index.db")
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var hash sql.NullString
	require.NoError(t, db.QueryRow(`SELECT git_refs_snapshot_hash FROM index_meta WHERE id = 1;`).Scan(&hash))
	if !hash.Valid {
		return ""
	}
	return strings.TrimSpace(hash.String)
}

func runSubtaskCLI(t *testing.T, binPath, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "subtask %v failed: %s", args, out)
	return string(out)
}

func TestExternalMergeDetection_ListShowsMerged_AncestorAndSnapshotInvalidation(t *testing.T) {
	env := testutil.NewTestEnv(t, 1)
	taskName := "ext/ancestor"

	env.CreateTask(taskName, "External ancestor merge", "main", "test")
	env.CreateTaskHistory(taskName, []history.Event{
		{Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})},
	})

	ws := env.Workspaces[0]
	gitCmd(t, ws, "checkout", "-b", taskName)
	f := filepath.Join(ws, "feature.txt")
	require.NoError(t, os.WriteFile(f, []byte("hello\n"), 0o644))
	gitCmd(t, ws, "add", "feature.txt")
	gitCmd(t, ws, "commit", "-m", "Add feature")

	bin := buildSubtask(t)

	// First list persists a snapshot (no repair pass).
	out1 := runSubtaskCLI(t, bin, env.RootDir, "list")
	assert.Contains(t, out1, taskName)
	hash1 := refsSnapshotHash(t, env.RootDir)
	require.NotEmpty(t, hash1)

	// External (history-preserving) merge.
	gitCmd(t, env.RootDir, "checkout", "main")
	gitCmd(t, env.RootDir, "merge", "--no-ff", taskName, "-m", "Merge "+taskName)

	out2 := runSubtaskCLI(t, bin, env.RootDir, "list")
	assert.Contains(t, out2, taskName)
	assert.Contains(t, out2, "✓ merged")

	hash2 := refsSnapshotHash(t, env.RootDir)
	require.NotEmpty(t, hash2)
	require.NotEqual(t, hash1, hash2)

	// No changes: snapshot hash stays stable.
	out3 := runSubtaskCLI(t, bin, env.RootDir, "list")
	assert.Contains(t, out3, taskName)
	assert.Contains(t, out3, "✓ merged")
	hash3 := refsSnapshotHash(t, env.RootDir)
	require.Equal(t, hash2, hash3)
}

func TestExternalMergeDetection_ListShowsMerged_SquashMerge(t *testing.T) {
	env := testutil.NewTestEnv(t, 1)
	taskName := "ext/squash"

	env.CreateTask(taskName, "External squash merge", "main", "test")
	env.CreateTaskHistory(taskName, []history.Event{
		{Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})},
	})

	ws := env.Workspaces[0]
	gitCmd(t, ws, "checkout", "-b", taskName)
	f := filepath.Join(ws, "squash.txt")
	require.NoError(t, os.WriteFile(f, []byte("squashed\n"), 0o644))
	gitCmd(t, ws, "add", "squash.txt")
	gitCmd(t, ws, "commit", "-m", "Add squashed file")

	bin := buildSubtask(t)

	// Prime snapshot.
	_ = runSubtaskCLI(t, bin, env.RootDir, "list")
	require.NotEmpty(t, refsSnapshotHash(t, env.RootDir))

	// External squash merge.
	gitCmd(t, env.RootDir, "checkout", "main")
	gitCmd(t, env.RootDir, "merge", "--squash", taskName)
	gitCmd(t, env.RootDir, "commit", "-m", "Squash "+taskName)

	out := runSubtaskCLI(t, bin, env.RootDir, "list")
	assert.Contains(t, out, taskName)
	assert.Contains(t, out, "✓ merged")
}

func TestExternalMergeDetection_Revocability_BranchAdvancesClearsMerged(t *testing.T) {
	env := testutil.NewTestEnv(t, 1)
	taskName := "ext/revocable"

	env.CreateTask(taskName, "Revocable", "main", "test")
	env.CreateTaskHistory(taskName, []history.Event{
		{Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})},
	})

	ws := env.Workspaces[0]
	gitCmd(t, ws, "checkout", "-b", taskName)
	f := filepath.Join(ws, "rev.txt")
	require.NoError(t, os.WriteFile(f, []byte("v1\n"), 0o644))
	gitCmd(t, ws, "add", "rev.txt")
	gitCmd(t, ws, "commit", "-m", "v1")

	// External merge.
	gitCmd(t, env.RootDir, "checkout", "main")
	gitCmd(t, env.RootDir, "merge", "--no-ff", taskName, "-m", "Merge "+taskName)

	bin := buildSubtask(t)
	out1 := runSubtaskCLI(t, bin, env.RootDir, "list")
	assert.Contains(t, out1, taskName)
	assert.Contains(t, out1, "✓ merged")

	// Branch advances after integration.
	gitCmd(t, ws, "checkout", taskName)
	require.NoError(t, os.WriteFile(f, []byte("v2\n"), 0o644))
	gitCmd(t, ws, "add", "rev.txt")
	gitCmd(t, ws, "commit", "-m", "v2")

	out2 := runSubtaskCLI(t, bin, env.RootDir, "list")
	assert.Contains(t, out2, taskName)
	assert.NotContains(t, out2, "✓ merged")
}

func TestExternalMergeDetection_ClosedTaskAutoPromotesToMerged(t *testing.T) {
	env := testutil.NewTestEnv(t, 1)
	taskName := "ext/closed-promote"

	env.CreateTask(taskName, "Closed promote", "main", "test")
	env.CreateTaskHistory(taskName, []history.Event{
		{Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})},
		{Type: "task.closed", Data: mustJSON(map[string]any{"reason": "close"})},
	})

	ws := env.Workspaces[0]
	gitCmd(t, ws, "checkout", "-b", taskName)
	f := filepath.Join(ws, "closed.txt")
	require.NoError(t, os.WriteFile(f, []byte("c\n"), 0o644))
	gitCmd(t, ws, "add", "closed.txt")
	gitCmd(t, ws, "commit", "-m", "c")

	// External merge into main.
	gitCmd(t, env.RootDir, "checkout", "main")
	gitCmd(t, env.RootDir, "merge", "--no-ff", taskName, "-m", "Merge "+taskName)

	bin := buildSubtask(t)
	out := runSubtaskCLI(t, bin, env.RootDir, "list")
	assert.Contains(t, out, taskName)
	assert.Contains(t, out, "✓ merged")

	tail, err := history.Tail(taskName)
	require.NoError(t, err)
	assert.Equal(t, task.TaskStatusMerged, tail.TaskStatus)
	assert.NotEmpty(t, tail.LastMergedCommit)
}

func TestExternalMergeDetection_BaseForcePushed_RemovesMergedDetection(t *testing.T) {
	env := testutil.NewTestEnv(t, 1)
	taskName := "ext/force-push"

	env.CreateTask(taskName, "Force push", "main", "test")
	env.CreateTaskHistory(taskName, []history.Event{
		{Type: "task.opened", Data: mustJSON(map[string]any{"reason": "draft", "base_branch": "main"})},
	})

	ws := env.Workspaces[0]
	gitCmd(t, ws, "checkout", "-b", taskName)
	f := filepath.Join(ws, "fp.txt")
	require.NoError(t, os.WriteFile(f, []byte("fp\n"), 0o644))
	gitCmd(t, ws, "add", "fp.txt")
	gitCmd(t, ws, "commit", "-m", "fp")

	// External merge.
	mainBefore := strings.TrimSpace(gitCmd(t, env.RootDir, "rev-parse", "main"))
	gitCmd(t, env.RootDir, "checkout", "main")
	gitCmd(t, env.RootDir, "merge", "--no-ff", taskName, "-m", "Merge "+taskName)

	bin := buildSubtask(t)
	out1 := runSubtaskCLI(t, bin, env.RootDir, "list")
	assert.Contains(t, out1, taskName)
	assert.Contains(t, out1, "✓ merged")

	// Force-push style rewrite: drop the merge commit.
	gitCmd(t, env.RootDir, "reset", "--hard", mainBefore)

	out2 := runSubtaskCLI(t, bin, env.RootDir, "list")
	assert.Contains(t, out2, taskName)
	assert.NotContains(t, out2, "✓ merged")
}
