package gitredesign_test

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/task"
	taskhistory "github.com/zippoxer/subtask/pkg/task/history"
	taskmigrate "github.com/zippoxer/subtask/pkg/task/migrate"
	"github.com/zippoxer/subtask/pkg/task/migrate/gitredesign"
)

func TestEnsure_V011Fixtures_E2E(t *testing.T) {
	t.Setenv("SUBTASK_DIR", t.TempDir())

	fixturesDir := testdataDir(t, "v0.1.1")
	bundlePath := filepath.Join(fixturesDir, "repo.bundle")
	subtaskTar := filepath.Join(fixturesDir, "subtask-dir.tar.gz")

	root := t.TempDir()
	repoDir := filepath.Join(root, "repo")

	require.NoError(t, os.MkdirAll(repoDir, 0o755))
	gitRun(t, repoDir, "init")
	// Fetch all fixture branches into a non-checked-out namespace, then create local branches.
	gitRun(t, repoDir, "fetch", bundlePath, "refs/heads/*:refs/remotes/bundle/*")
	gitRun(t, repoDir, "checkout", "-B", "main", "refs/remotes/bundle/main")
	gitRun(t, repoDir, "branch", "legacy/merged", "refs/remotes/bundle/legacy/merged")
	gitRun(t, repoDir, "branch", "legacy/closed-keep", "refs/remotes/bundle/legacy/closed-keep")
	untarGz(t, subtaskTar, repoDir)

	origCwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(repoDir))
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	require.NoError(t, taskmigrate.EnsureLayout(repoDir))
	require.NoError(t, gitredesign.Ensure(repoDir))

	markerPath := filepath.Join(task.ProjectsDir(), task.EscapePath(repoDir), "migrations", "gitredesign-v1.done")
	require.FileExists(t, markerPath)

	taskNames, err := task.List()
	require.NoError(t, err)
	require.NotEmpty(t, taskNames)

	// Validate every task generally (schema + base_commit/base_ref on opened).
	for _, name := range taskNames {
		t.Run("task="+strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			loaded, err := task.Load(name)
			require.NoError(t, err)
			require.Equal(t, gitredesign.TaskSchemaVersion, loaded.Schema)

			events, err := taskhistory.Read(name, taskhistory.ReadOptions{})
			require.NoError(t, err)
			require.NotEmpty(t, events)

			opened := lastEventOfType(t, events, "task.opened")
			var openedData map[string]any
			require.NoError(t, json.Unmarshal(opened.Data, &openedData))

			baseCommit, _ := openedData["base_commit"].(string)
			baseRef, _ := openedData["base_ref"].(string)
			require.Regexp(t, "^[0-9a-f]{40}$", strings.TrimSpace(baseCommit))
			require.NotEmpty(t, strings.TrimSpace(baseRef))
			gitRun(t, repoDir, "cat-file", "-e", strings.TrimSpace(baseCommit)+"^{commit}")

			for _, evType := range []string{"task.merged", "task.closed"} {
				ev := lastEventOfTypeOrNil(events, evType)
				if ev == nil {
					continue
				}
				require.False(t, ev.TS.IsZero())

				var data map[string]any
				require.NoError(t, json.Unmarshal(ev.Data, &data))
				_, hasAdded := data["changes_added"]
				_, hasErr := data["frozen_error"]
				require.True(t, hasAdded || hasErr)
			}
		})
	}

	// Specific timestamp backfill assertions for the v0.1.1 fixture set.
	assertMergedTimestampFromCommitDate(t, "legacy/merged", "877f967876cb188d569d8135874d65b4e7c7238a")
	assertClosedTimestampFromBranchHeadCommitDate(t, "legacy/closed-keep", "55298ad2cbb4c5c9477189b9049555582cc35bb0")

	// With the marker present, Ensure should not depend on task scanning at all.
	tasksDir := filepath.Join(repoDir, ".subtask", "tasks")
	bak := tasksDir + ".bak"
	require.NoError(t, os.Rename(tasksDir, bak))
	require.NoError(t, os.WriteFile(tasksDir, []byte("not a dir"), 0o644))
	require.NoError(t, gitredesign.Ensure(repoDir))
	require.NoError(t, os.Remove(tasksDir))
	require.NoError(t, os.Rename(bak, tasksDir))

	// Without the marker, Ensure should still be idempotent for already-migrated tasks.
	before := readAllHistories(t, repoDir)
	require.NoError(t, os.Remove(markerPath))
	require.NoError(t, gitredesign.Ensure(repoDir))
	after := readAllHistories(t, repoDir)
	require.Equal(t, before, after)
	require.FileExists(t, markerPath)
}

func assertMergedTimestampFromCommitDate(t *testing.T, taskName, commit string) {
	t.Helper()
	events, err := taskhistory.Read(taskName, taskhistory.ReadOptions{})
	require.NoError(t, err)
	merged := lastEventOfType(t, events, "task.merged")

	expected := gitCommitDateE2E(t, task.ProjectRoot(), commit)
	require.True(t, merged.TS.Equal(expected))
}

func assertClosedTimestampFromBranchHeadCommitDate(t *testing.T, taskName, headCommit string) {
	t.Helper()
	events, err := taskhistory.Read(taskName, taskhistory.ReadOptions{})
	require.NoError(t, err)
	closed := lastEventOfType(t, events, "task.closed")

	expected := gitCommitDateE2E(t, task.ProjectRoot(), headCommit)
	require.True(t, closed.TS.Equal(expected))
}

func readAllHistories(t *testing.T, repoDir string) map[string]string {
	t.Helper()
	out := map[string]string{}

	taskNames, err := task.List()
	require.NoError(t, err)
	for _, name := range taskNames {
		b, err := os.ReadFile(filepath.Join(repoDir, ".subtask", "tasks", task.EscapeName(name), "history.jsonl"))
		require.NoError(t, err)
		out[name] = string(b)
	}
	return out
}

func lastEventOfType(t *testing.T, events []taskhistory.Event, typ string) taskhistory.Event {
	t.Helper()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == typ {
			return events[i]
		}
	}
	t.Fatalf("missing event type %q", typ)
	return taskhistory.Event{}
}

func lastEventOfTypeOrNil(events []taskhistory.Event, typ string) *taskhistory.Event {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == typ {
			ev := events[i]
			return &ev
		}
	}
	return nil
}

func gitCommitDateE2E(t *testing.T, repoDir, commit string) time.Time {
	t.Helper()
	out := gitRun(t, repoDir, "show", "-s", "--format=%cI", commit)
	ts, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(out))
	require.NoError(t, err)
	return ts.UTC()
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	b, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s failed: %s", strings.Join(args, " "), string(b))
	return strings.TrimSpace(string(b))
}

func testdataDir(t *testing.T, subdir string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(thisFile), "testdata", subdir)
}

func untarGz(t *testing.T, src, dst string) {
	t.Helper()

	f, err := os.Open(src)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	zr, err := gzip.NewReader(f)
	require.NoError(t, err)
	defer func() { _ = zr.Close() }()

	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
		}
		if hdr == nil {
			break
		}

		name := filepath.Clean(hdr.Name)
		if name == "." || name == "" {
			continue
		}
		if strings.HasPrefix(name, ".."+string(os.PathSeparator)) || name == ".." || filepath.IsAbs(name) {
			t.Fatalf("invalid tar entry: %q", hdr.Name)
		}

		outPath := filepath.Join(dst, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			require.NoError(t, os.MkdirAll(outPath, 0o755))
		case tar.TypeReg:
			require.NoError(t, os.MkdirAll(filepath.Dir(outPath), 0o755))
			w, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			require.NoError(t, err)
			_, copyErr := io.Copy(w, tr)
			closeErr := w.Close()
			require.NoError(t, copyErr)
			require.NoError(t, closeErr)
		default:
			// Ignore symlinks/other types (fixtures should not need them).
		}
	}
}
