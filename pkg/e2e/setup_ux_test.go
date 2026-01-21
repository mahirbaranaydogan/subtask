package e2e

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

	t.Run("FreshInstall_NoInit_DraftAndListWork", func(t *testing.T) {
		subtaskDir := t.TempDir()
		t.Setenv("SUBTASK_DIR", subtaskDir)

		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home) // windows

		addStubCommandToPATH(t, "codex")

		// Install + configure (writes global config).
		cwd := t.TempDir()
		out, err := runSubtaskWithErr(t, bin, cwd, "install", "--no-prompt")
		require.NoError(t, err, out)
		require.FileExists(t, task.ConfigPath())

		// New repo: draft/list should work without any init ceremony.
		repo := t.TempDir()
		initGitRepo(t, repo)

		taskName := "setup/test"
		out, err = runSubtaskWithErr(t, bin, repo, "draft", taskName, "desc", "--base-branch", "main", "--title", "Setup UX")
		require.NoError(t, err, out)
		require.FileExists(t, filepath.Join(repo, ".subtask", "tasks", task.EscapeName(taskName), "TASK.md"))

		out, err = runSubtaskWithErr(t, bin, repo, "list")
		require.NoError(t, err, out)
		require.Contains(t, out, taskName)
	})

	t.Run("ConfigProject_NoPrompt_CreatesOverrideFile", func(t *testing.T) {
		subtaskDir := t.TempDir()
		t.Setenv("SUBTASK_DIR", subtaskDir)

		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home) // windows

		addStubCommandToPATH(t, "codex")

		// Global config present.
		cfg := &workspace.Config{Harness: "builtin-mock", MaxWorkspaces: 3}
		cfgData, _ := json.MarshalIndent(cfg, "", "  ")
		require.NoError(t, os.MkdirAll(filepath.Dir(task.ConfigPath()), 0o755))
		require.NoError(t, os.WriteFile(task.ConfigPath(), cfgData, 0o644))

		repo := t.TempDir()
		initGitRepo(t, repo)

		out, err := runSubtaskWithErr(t, bin, repo, "config", "--project", "--no-prompt")
		require.NoError(t, err, out)
		require.FileExists(t, filepath.Join(repo, ".subtask", "config.json"))
	})

	t.Run("ConfigProject_NoPrompt_FlagsOverride", func(t *testing.T) {
		subtaskDir := t.TempDir()
		t.Setenv("SUBTASK_DIR", subtaskDir)
		t.Setenv("SUBTASK_DEBUG", "")

		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home) // windows

		addStubCommandToPATH(t, "codex")

		// Global config present, but should not influence flag-driven project config.
		cfg := &workspace.Config{Harness: "builtin-mock", MaxWorkspaces: 3}
		cfgData, _ := json.MarshalIndent(cfg, "", "  ")
		require.NoError(t, os.MkdirAll(filepath.Dir(task.ConfigPath()), 0o755))
		require.NoError(t, os.WriteFile(task.ConfigPath(), cfgData, 0o644))

		repo := t.TempDir()
		initGitRepo(t, repo)

		out, err := runSubtaskWithErr(t, bin, repo,
			"config", "--project", "--no-prompt",
			"--harness", "codex",
			"--model", "gpt-5.2-codex",
			"--reasoning", "medium",
			"--max-workspaces", "9",
		)
		require.NoError(t, err, out)

		var got workspace.Config
		require.NoError(t, readJSON(filepath.Join(repo, ".subtask", "config.json"), &got))
		require.Equal(t, "codex", got.Harness)
		require.Equal(t, 9, got.MaxWorkspaces)
		require.Equal(t, "gpt-5.2-codex", got.Options["model"])
		require.Equal(t, "medium", got.Options["reasoning"])
	})

	t.Run("Migration_NoClobber_WhenDestinationExists", func(t *testing.T) {
		subtaskDir := t.TempDir()
		t.Setenv("SUBTASK_DIR", subtaskDir)

		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home) // windows

		// Global config present so migration won't promote legacy repo config.
		cfg := &workspace.Config{Harness: "builtin-mock", MaxWorkspaces: 3}
		cfgData, _ := json.MarshalIndent(cfg, "", "  ")
		require.NoError(t, os.MkdirAll(filepath.Dir(task.ConfigPath()), 0o755))
		require.NoError(t, os.WriteFile(task.ConfigPath(), cfgData, 0o644))

		repo := t.TempDir()
		initGitRepo(t, repo)

		// Seed repo with legacy runtime layout.
		fixture := filepath.Join("..", "task", "migrate", "testdata", "legacy", "basic")
		require.NoError(t, copyDir(fixture, filepath.Join(repo, ".subtask")))

		projectDir := filepath.Join(task.ProjectsDir(), task.EscapePath(repo))
		destInternalDir := filepath.Join(projectDir, "internal", "legacy--basic")
		require.NoError(t, os.MkdirAll(destInternalDir, 0o755))
		destStatePath := filepath.Join(destInternalDir, "state.json")
		require.NoError(t, os.WriteFile(destStatePath, []byte(`{"session_id":"dest"}`+"\n"), 0o644))

		// Create destination index.db (valid) and tag it with a sentinel table.
		destIndex := filepath.Join(projectDir, "index.db")
		require.NoError(t, copyFile(filepath.Join(repo, ".subtask", "index.db"), destIndex))
		db, err := sql.Open("sqlite", destIndex)
		require.NoError(t, err)
		_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS sentinel (value TEXT);`)
		_, _ = db.Exec(`DELETE FROM sentinel;`)
		_, _ = db.Exec(`INSERT INTO sentinel(value) VALUES ('dest');`)
		require.NoError(t, db.Close())

		// Corrupt the legacy index and state; migration must not overwrite destination.
		require.NoError(t, os.WriteFile(filepath.Join(repo, ".subtask", "index.db"), []byte("legacy-corrupt"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(repo, ".subtask", "internal", "legacy--basic", "state.json"), []byte(`{"session_id":"legacy"}`+"\n"), 0o644))

		out, err := runSubtaskWithErr(t, bin, repo, "list")
		require.NoError(t, err, out)

		// Destination state should be unchanged (no clobber).
		gotState, err := os.ReadFile(destStatePath)
		require.NoError(t, err)
		require.Contains(t, string(gotState), `"session_id":"dest"`)

		// Destination index should not have been replaced/rebuilt (sentinel preserved).
		db, err = sql.Open("sqlite", destIndex)
		require.NoError(t, err)
		var v string
		require.NoError(t, db.QueryRow(`SELECT value FROM sentinel LIMIT 1;`).Scan(&v))
		require.Equal(t, "dest", v)
		require.NoError(t, db.Close())

		// Legacy runtime should be removed from repo after migration.
		_, err = os.Stat(filepath.Join(repo, ".subtask", "internal"))
		require.True(t, os.IsNotExist(err))
		_, err = os.Stat(filepath.Join(repo, ".subtask", "index.db"))
		require.True(t, os.IsNotExist(err))
	})

	t.Run("ConfigScope_ProjectOverrideWins_AndOtherRepoUsesGlobal", func(t *testing.T) {
		subtaskDir := t.TempDir()
		t.Setenv("SUBTASK_DIR", subtaskDir)

		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home) // windows

		// Repo A has a project override that switches to the external mock harness.
		repoA := t.TempDir()
		initGitRepo(t, repoA)
		require.NoError(t, os.MkdirAll(filepath.Join(repoA, ".subtask"), 0o755))
		workerPath := mockWorkerPathForSubtask(bin)
		require.FileExists(t, workerPath)

		projectCfg := &workspace.Config{
			Harness: "mock",
			Options: map[string]any{"cli": workerPath},
		}
		b, _ := json.MarshalIndent(projectCfg, "", "  ")
		require.NoError(t, os.WriteFile(filepath.Join(repoA, ".subtask", "config.json"), b, 0o644))

		// Global defaults: builtin mock (in-process).
		globalCfg := &workspace.Config{
			Harness:       "builtin-mock",
			MaxWorkspaces: 3,
			Options:       map[string]any{"tool_calls": 0},
		}
		gb, _ := json.MarshalIndent(globalCfg, "", "  ")
		require.NoError(t, os.MkdirAll(filepath.Dir(task.ConfigPath()), 0o755))
		require.NoError(t, os.WriteFile(task.ConfigPath(), gb, 0o644))

		out, err := runSubtaskWithErr(t, bin, repoA, "ask", "hi")
		require.NoError(t, err, out)
		require.Contains(t, out, "Mock completed (no commands).")

		// Repo B should use global defaults (no project override).
		repoB := t.TempDir()
		initGitRepo(t, repoB)

		out, err = runSubtaskWithErr(t, bin, repoB, "ask", "hi")
		require.NoError(t, err, out)
		require.Contains(t, out, "Mock response for:")
	})

	t.Run("Worktree_AutoResolvesAnchor", func(t *testing.T) {
		subtaskDir := t.TempDir()
		t.Setenv("SUBTASK_DIR", subtaskDir)

		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home) // windows

		// Global config present.
		cfg := &workspace.Config{Harness: "builtin-mock", MaxWorkspaces: 3}
		cfgData, _ := json.MarshalIndent(cfg, "", "  ")
		require.NoError(t, os.MkdirAll(filepath.Dir(task.ConfigPath()), 0o755))
		require.NoError(t, os.WriteFile(task.ConfigPath(), cfgData, 0o644))

		anchor := t.TempDir()
		initGitRepo(t, anchor)
		require.NoError(t, os.MkdirAll(filepath.Join(anchor, ".subtask", "tasks"), 0o755)) // helps anchor selection

		escaped := task.EscapePath(anchor)
		wsPath := filepath.Join(task.WorkspacesDir(), fmt.Sprintf("%s--%d", escaped, 1))
		require.NoError(t, os.MkdirAll(filepath.Dir(wsPath), 0o755))
		run(t, anchor, "git", "worktree", "add", "--detach", wsPath)

		out, err := runSubtaskWithErr(t, bin, wsPath, "list")
		require.NoError(t, err, out)

		// Runtime folder should be for the anchor, not the workspace root.
		require.DirExists(t, filepath.Join(task.ProjectsDir(), task.EscapePath(anchor)))
		_, err = os.Stat(filepath.Join(task.ProjectsDir(), task.EscapePath(wsPath)))
		require.True(t, os.IsNotExist(err))
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

func addStubCommandToPATH(t *testing.T, name string) {
	t.Helper()

	binDir := t.TempDir()
	var path string
	var content []byte

	if runtime.GOOS == "windows" {
		path = filepath.Join(binDir, name+".bat")
		content = []byte("@echo off\r\nexit /B 0\r\n")
	} else {
		path = filepath.Join(binDir, name)
		content = []byte("#!/bin/sh\nexit 0\n")
	}
	require.NoError(t, os.WriteFile(path, content, 0o755))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
