package migrate

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/task"
)

func TestEnsureLayout_LegacyFixtureBasic(t *testing.T) {
	t.Setenv("SUBTASK_DIR", t.TempDir())

	repoRoot := t.TempDir()
	require.NoError(t, copyDir(filepath.Join("testdata", "legacy", "basic"), filepath.Join(repoRoot, ".subtask")))

	require.NoError(t, EnsureLayout(repoRoot))

	// Promoted global config exists.
	require.FileExists(t, task.ConfigPath())

	// Runtime state moved to ~/.subtask/projects/<escaped-git-root>/...
	projectDir := filepath.Join(task.ProjectsDir(), task.EscapePath(repoRoot))
	require.FileExists(t, filepath.Join(projectDir, "internal", "legacy--basic", "state.json"))
	require.FileExists(t, filepath.Join(projectDir, "internal", "legacy--basic", "progress.json"))
	require.FileExists(t, filepath.Join(projectDir, "index.db"))

	// Repo cleanup: legacy runtime state removed.
	_, err := os.Stat(filepath.Join(repoRoot, ".subtask", "internal"))
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(repoRoot, ".subtask", "index.db"))
	require.True(t, os.IsNotExist(err))

	// Portable data stays.
	require.FileExists(t, filepath.Join(repoRoot, ".subtask", "tasks", "legacy--basic", "TASK.md"))
	require.FileExists(t, filepath.Join(repoRoot, ".subtask", "config.json"))

	// Idempotent.
	require.NoError(t, EnsureLayout(repoRoot))
}

func TestEnsureLayout_LegacyFixtureDraftOnly_NoIndex(t *testing.T) {
	t.Setenv("SUBTASK_DIR", t.TempDir())

	repoRoot := t.TempDir()
	require.NoError(t, copyDir(filepath.Join("testdata", "legacy", "draft-only"), filepath.Join(repoRoot, ".subtask")))

	require.NoError(t, EnsureLayout(repoRoot))

	projectDir := filepath.Join(task.ProjectsDir(), task.EscapePath(repoRoot))
	require.FileExists(t, filepath.Join(projectDir, "internal", "legacy--draftonly", "op.lock"))

	// No legacy index => no runtime index created by layout migration.
	_, err := os.Stat(filepath.Join(projectDir, "index.db"))
	require.True(t, os.IsNotExist(err))

	// Repo cleanup.
	_, err = os.Stat(filepath.Join(repoRoot, ".subtask", "internal"))
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(repoRoot, ".subtask", "index.db"))
	require.True(t, os.IsNotExist(err))
}

func TestEnsureLayout_LegacyFixtureMulti(t *testing.T) {
	t.Setenv("SUBTASK_DIR", t.TempDir())

	repoRoot := t.TempDir()
	require.NoError(t, copyDir(filepath.Join("testdata", "legacy", "multi"), filepath.Join(repoRoot, ".subtask")))

	require.NoError(t, EnsureLayout(repoRoot))

	projectDir := filepath.Join(task.ProjectsDir(), task.EscapePath(repoRoot))
	require.FileExists(t, filepath.Join(projectDir, "internal", "legacy--open", "state.json"))
	require.FileExists(t, filepath.Join(projectDir, "internal", "legacy--open", "progress.json"))
	require.FileExists(t, filepath.Join(projectDir, "internal", "legacy--closed", "state.json"))
	require.FileExists(t, filepath.Join(projectDir, "internal", "legacy--merged", "state.json"))
	require.FileExists(t, filepath.Join(projectDir, "index.db"))

	// Repo cleanup.
	_, err := os.Stat(filepath.Join(repoRoot, ".subtask", "internal"))
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(repoRoot, ".subtask", "index.db"))
	require.True(t, os.IsNotExist(err))

	// Portable history remains.
	require.FileExists(t, filepath.Join(repoRoot, ".subtask", "tasks", "legacy--merged", "history.jsonl"))
	require.FileExists(t, filepath.Join(repoRoot, ".subtask", "tasks", "legacy--closed", "history.jsonl"))
	require.FileExists(t, filepath.Join(repoRoot, ".subtask", "tasks", "legacy--open", "history.jsonl"))
}

func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, st.Mode())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
