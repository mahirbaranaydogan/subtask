package e2e

import (
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/task"
	taskindex "github.com/zippoxer/subtask/pkg/task/index"
	"github.com/zippoxer/subtask/pkg/workspace"
)

func TestSetupUX(t *testing.T) {
	bin := buildSubtask(t)

	t.Run("NotConfigured_ListFails", func(t *testing.T) {
		t.Setenv("SUBTASK_DIR", t.TempDir())
		root := t.TempDir()
		run(t, root, "git", "init", "-b", "main")
		run(t, root, "git", "config", "user.email", "test@test.com")
		run(t, root, "git", "config", "user.name", "Test User")

		out, err := runSubtaskWithErr(t, bin, root, "list")
		require.Error(t, err)
		require.Contains(t, out, "subtask: not configured — run 'subtask install' first")
	})

	t.Run("NotGitRepo_ListFails", func(t *testing.T) {
		t.Setenv("SUBTASK_DIR", t.TempDir())
		dir := t.TempDir()

		out, err := runSubtaskWithErr(t, bin, dir, "list")
		require.Error(t, err)
		require.Contains(t, out, "subtask: not a git repository — subtask requires git")
	})

	t.Run("LegacyMigration_PromotesConfigAndRuntime", func(t *testing.T) {
		subtaskDir := t.TempDir()
		t.Setenv("SUBTASK_DIR", subtaskDir)

		root := t.TempDir()
		run(t, root, "git", "init", "-b", "main")
		run(t, root, "git", "config", "user.email", "test@test.com")
		run(t, root, "git", "config", "user.name", "Test User")
		_ = os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".subtask/\n"), 0o644)
		_ = os.WriteFile(filepath.Join(root, "README.md"), []byte("# test\n"), 0o644)
		run(t, root, "git", "add", ".")
		run(t, root, "git", "commit", "-m", "init")

		// Golden legacy repo-local layout produced by old Subtask CLI.
		fixture := filepath.Join("..", "task", "migrate", "testdata", "legacy", "basic")
		require.NoError(t, copyDir(fixture, filepath.Join(root, ".subtask")))

		out, err := runSubtaskWithErr(t, bin, root, "list")
		require.NoError(t, err, out)

		// Global config should be created.
		require.FileExists(t, task.ConfigPath())

		// Runtime state should exist in ~/.subtask/projects/<escaped-git-root>/...
		projectDir := filepath.Join(task.ProjectsDir(), task.EscapePath(root))
		require.FileExists(t, filepath.Join(projectDir, "internal", "legacy--basic", "state.json"))
		require.FileExists(t, filepath.Join(projectDir, "index.db"))

		// Legacy runtime state should be removed from the repo.
		_, err = os.Stat(filepath.Join(root, ".subtask", "internal"))
		require.True(t, os.IsNotExist(err))
		_, err = os.Stat(filepath.Join(root, ".subtask", "index.db"))
		require.True(t, os.IsNotExist(err))

		// Portable data stays in the repo.
		require.FileExists(t, filepath.Join(root, ".subtask", "tasks", "legacy--basic", "TASK.md"))
		require.FileExists(t, filepath.Join(root, ".subtask", "config.json"))

		// Index should be usable even if legacy file was corrupt (rebuilt is OK).
		idx, err := taskindex.Open(filepath.Join(projectDir, "index.db"))
		require.NoError(t, err)
		require.NoError(t, idx.Close())
		db, err := sql.Open("sqlite", filepath.Join(projectDir, "index.db"))
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		var hash sql.NullString
		require.NoError(t, db.QueryRow(`SELECT git_refs_snapshot_hash FROM index_meta WHERE id = 1;`).Scan(&hash))
	})

	t.Run("SubdirUsage_ListWorks", func(t *testing.T) {
		subtaskDir := t.TempDir()
		t.Setenv("SUBTASK_DIR", subtaskDir)

		root := t.TempDir()
		run(t, root, "git", "init", "-b", "main")
		run(t, root, "git", "config", "user.email", "test@test.com")
		run(t, root, "git", "config", "user.name", "Test User")
		_ = os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".subtask/\n"), 0o644)

		// Global config present.
		cfg := &workspace.Config{Harness: "mock", MaxWorkspaces: 3}
		cfgData, _ := json.MarshalIndent(cfg, "", "  ")
		require.NoError(t, os.MkdirAll(filepath.Dir(task.ConfigPath()), 0o755))
		require.NoError(t, os.WriteFile(task.ConfigPath(), cfgData, 0o644))

		subdir := filepath.Join(root, "src", "foo")
		require.NoError(t, os.MkdirAll(subdir, 0o755))

		out, err := runSubtaskWithErr(t, bin, subdir, "list")
		require.NoError(t, err, out)
	})

	t.Run("InvalidGlobalConfig_ListErrorsHelpful", func(t *testing.T) {
		t.Setenv("SUBTASK_DIR", t.TempDir())

		root := t.TempDir()
		run(t, root, "git", "init", "-b", "main")
		run(t, root, "git", "config", "user.email", "test@test.com")
		run(t, root, "git", "config", "user.name", "Test User")

		require.NoError(t, os.MkdirAll(filepath.Dir(task.ConfigPath()), 0o755))
		require.NoError(t, os.WriteFile(task.ConfigPath(), []byte("{\n"), 0o644))

		out, err := runSubtaskWithErr(t, bin, root, "list")
		require.Error(t, err)
		require.Contains(t, out, "subtask: invalid config")
		require.Contains(t, out, "subtask config --user")
	})

	t.Run("InvalidProjectConfig_ListErrorsHelpful", func(t *testing.T) {
		t.Setenv("SUBTASK_DIR", t.TempDir())

		root := t.TempDir()
		run(t, root, "git", "init", "-b", "main")
		run(t, root, "git", "config", "user.email", "test@test.com")
		run(t, root, "git", "config", "user.name", "Test User")

		// Valid global config.
		cfg := &workspace.Config{Harness: "mock", MaxWorkspaces: 3}
		cfgData, _ := json.MarshalIndent(cfg, "", "  ")
		require.NoError(t, os.MkdirAll(filepath.Dir(task.ConfigPath()), 0o755))
		require.NoError(t, os.WriteFile(task.ConfigPath(), cfgData, 0o644))

		// Invalid project override.
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".subtask"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, ".subtask", "config.json"), []byte("{\n"), 0o644))

		out, err := runSubtaskWithErr(t, bin, root, "list")
		require.Error(t, err)
		require.Contains(t, out, "invalid project config")
		require.Contains(t, out, "subtask config --project")
	})
}

func runSubtaskWithErr(t *testing.T, binPath, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
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
