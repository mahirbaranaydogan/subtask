package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/render"
)

func TestInstallStatusUninstall_UserScope_NoPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUBTASK_DIR", filepath.Join(home, ".subtask"))

	cwd := t.TempDir()
	prev, _ := os.Getwd()
	require.NoError(t, os.Chdir(cwd))
	t.Cleanup(func() { _ = os.Chdir(prev) })

	// Ensure at least one harness is "available" so `subtask install --no-prompt`
	// can write a usable ~/.subtask/config.json.
	binDir := filepath.Join(cwd, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	_ = writeFakeCLI(t, binDir, "codex")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	withOutputMode(t, false)
	render.Pretty = false

	stdout, stderr, err := captureStdoutStderr(t, (&StatusCmd{}).Run)
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "Skill installed: no")
	require.NotContains(t, stdout, "Plugin installed")

	_, stderr, err = captureStdoutStderr(t, (&InstallCmd{NoPrompt: true}).Run)
	require.NoError(t, err)
	require.Empty(t, stderr)

	stdout, stderr, err = captureStdoutStderr(t, (&StatusCmd{}).Run)
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "Skill installed: yes")
	require.NotContains(t, stdout, "Plugin installed")

	_, stderr, err = captureStdoutStderr(t, (&UninstallCmd{}).Run)
	require.NoError(t, err)
	require.Empty(t, stderr)

	stdout, stderr, err = captureStdoutStderr(t, (&StatusCmd{}).Run)
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "Skill installed: no")
	require.NotContains(t, stdout, "Plugin installed")
}
