package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/install"
)

func TestRunAutoUpdate_ProjectSkillOutdated_Warns(t *testing.T) {
	withOutputMode(t, false)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(autoUpdateEnvVar, "")

	repo := t.TempDir()
	gitCmd(t, repo, "init")

	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(repo))
	t.Cleanup(func() { _ = os.Chdir(prev) })

	projectSkill := filepath.Join(repo, ".claude", "skills", "subtask", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(projectSkill), 0o755))
	require.NoError(t, os.WriteFile(projectSkill, []byte("outdated"), 0o644))

	stdout, stderr, err := captureStdoutStderr(t, func() error {
		runAutoUpdate()
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, stdout)
	require.Equal(t, "warning: Project skill at "+filepath.Join(".claude", "skills", "subtask", "SKILL.md")+" is outdated. Run `subtask install --scope project` to update.\n", stderr)
}

func TestRunAutoUpdate_ProjectSkillUpToDate_Silent(t *testing.T) {
	withOutputMode(t, false)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(autoUpdateEnvVar, "")

	repo := t.TempDir()
	gitCmd(t, repo, "init")

	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(repo))
	t.Cleanup(func() { _ = os.Chdir(prev) })

	projectSkill := filepath.Join(repo, ".claude", "skills", "subtask", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(projectSkill), 0o755))
	require.NoError(t, os.WriteFile(projectSkill, install.Embedded(), 0o644))

	stdout, stderr, err := captureStdoutStderr(t, func() error {
		runAutoUpdate()
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, stdout)
	require.Empty(t, stderr)
}

func TestRunAutoUpdate_NoGitRepo_Silent(t *testing.T) {
	withOutputMode(t, false)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(autoUpdateEnvVar, "")

	dir := t.TempDir()

	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(prev) })

	// Even if a project-scope path exists here, project scope only applies inside a git repo.
	projectSkill := filepath.Join(dir, ".claude", "skills", "subtask", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(projectSkill), 0o755))
	require.NoError(t, os.WriteFile(projectSkill, []byte("outdated"), 0o644))

	stdout, stderr, err := captureStdoutStderr(t, func() error {
		runAutoUpdate()
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, stdout)
	require.Empty(t, stderr)
}

func TestRunAutoUpdate_AutoUpdateDisabled_SkipsChecks(t *testing.T) {
	withOutputMode(t, false)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(autoUpdateEnvVar, "1")

	repo := t.TempDir()
	gitCmd(t, repo, "init")

	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(repo))
	t.Cleanup(func() { _ = os.Chdir(prev) })

	projectSkill := filepath.Join(repo, ".claude", "skills", "subtask", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(projectSkill), 0o755))
	require.NoError(t, os.WriteFile(projectSkill, []byte("outdated"), 0o644))

	stdout, stderr, err := captureStdoutStderr(t, func() error {
		runAutoUpdate()
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, stdout)
	require.Empty(t, stderr)
}

