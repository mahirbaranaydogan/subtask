package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/subtaskerr"
	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/testutil"
	"github.com/zippoxer/subtask/pkg/workspace"
)

func TestConfigCmd_UserScope_NoPrompt_WritesGlobalConfig(t *testing.T) {
	t.Setenv("SUBTASK_DIR", t.TempDir())

	// Ensure at least one harness is "available".
	binDir := filepath.Join(t.TempDir(), "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	_ = writeFakeCLI(t, binDir, "codex")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	require.NoError(t, (&ConfigCmd{User: true, NoPrompt: true}).Run())

	b, err := os.ReadFile(task.ConfigPath())
	require.NoError(t, err)

	var cfg workspace.Config
	require.NoError(t, json.Unmarshal(b, &cfg))
	require.NotEmpty(t, cfg.Harness)
}

func TestConfigCmd_ProjectScope_RequiresGitRepo(t *testing.T) {
	t.Setenv("SUBTASK_DIR", t.TempDir())
	prev, _ := os.Getwd()
	cwd := t.TempDir()
	require.NoError(t, os.Chdir(cwd))
	t.Cleanup(func() { _ = os.Chdir(prev) })

	err := (&ConfigCmd{Project: true, NoPrompt: true}).Run()
	require.True(t, errors.Is(err, subtaskerr.ErrNotGitRepo))
}

func TestConfigCmd_ProjectScope_NoPrompt_WritesProjectOverride(t *testing.T) {
	t.Setenv("SUBTASK_DIR", t.TempDir())

	// Ensure at least one harness is "available".
	binDir := filepath.Join(t.TempDir(), "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	_ = writeFakeCLI(t, binDir, "codex")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	env := testutil.NewTestEnv(t, 0)

	require.NoError(t, (&ConfigCmd{Project: true, NoPrompt: true}).Run())
	require.FileExists(t, filepath.Join(env.RootDir, ".subtask", "config.json"))
}

