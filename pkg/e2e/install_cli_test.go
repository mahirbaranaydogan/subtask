package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/install"
	"github.com/zippoxer/subtask/pkg/workspace"
)

func TestInstall_UserScope_InstallsSkill_AndIsIdempotent(t *testing.T) {
	bin := buildSubtask(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	t.Setenv("SUBTASK_DIR", filepath.Join(home, ".subtask"))
	addStubCommandToPATH(t, "codex")
	cwd := t.TempDir()

	out := runSubtask(t, bin, cwd, home, "install", "--no-prompt")
	require.Contains(t, out, "Installed skill")

	// Skill path.
	skillPath := filepath.Join(home, ".claude", "skills", "subtask", "SKILL.md")
	gotSkill, err := os.ReadFile(skillPath)
	require.NoError(t, err)
	require.Equal(t, install.Embedded(), gotSkill)

	// Idempotent: second install shouldn't break settings or content.
	out2 := runSubtask(t, bin, cwd, home, "install", "--no-prompt")
	require.Contains(t, out2, "Skill already up to date")
}

func TestInstall_Migration_NoLegacyArtifacts_NoWritesToSettings(t *testing.T) {
	bin := buildSubtask(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	t.Setenv("SUBTASK_DIR", filepath.Join(home, ".subtask"))
	addStubCommandToPATH(t, "codex")
	cwd := t.TempDir()

	out := runSubtask(t, bin, cwd, home, "install", "--no-prompt")
	require.Contains(t, out, "Installed skill")

	// Migration must not create these.
	_, err := os.Stat(filepath.Join(home, ".claude", "settings.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(home, ".claude", "plugins", "subtask"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestInstall_Migration_RemovesLegacyPluginDir(t *testing.T) {
	bin := buildSubtask(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	t.Setenv("SUBTASK_DIR", filepath.Join(home, ".subtask"))
	addStubCommandToPATH(t, "codex")
	cwd := t.TempDir()

	legacyDir := filepath.Join(home, ".claude", "plugins", "subtask")
	require.NoError(t, os.MkdirAll(legacyDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "sentinel"), []byte("x"), 0o644))

	_ = runSubtask(t, bin, cwd, home, "install", "--no-prompt")

	_, err := os.Stat(legacyDir)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestInstall_Migration_RemovesLegacySettingsKeyOnly(t *testing.T) {
	bin := buildSubtask(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	subtaskDir := filepath.Join(home, ".subtask")
	t.Setenv("SUBTASK_DIR", subtaskDir)
	addStubCommandToPATH(t, "codex")
	cwd := t.TempDir()

	require.NoError(t, os.MkdirAll(subtaskDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subtaskDir, "config.json"), []byte(`{"harness":"codex","max_workspaces":1}`+"\n"), 0o644))

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(settingsPath), 0o755))
	require.NoError(t, os.WriteFile(settingsPath, []byte(`{"enabledPlugins":{"subtask":true,"other":true},"keep":123}`+"\n"), 0o644))

	_ = runSubtask(t, bin, cwd, home, "install", "--no-prompt")

	var settings map[string]any
	require.NoError(t, readJSON(settingsPath, &settings))

	enabled, ok := settings["enabledPlugins"].(map[string]any)
	require.True(t, ok, "enabledPlugins should remain an object")
	require.Equal(t, true, enabled["other"])
	require.Nil(t, enabled["subtask"])
	require.Equal(t, float64(123), settings["keep"])
}

func TestInstall_Migration_DoesNotRemoveMarketplaceKey(t *testing.T) {
	bin := buildSubtask(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	subtaskDir := filepath.Join(home, ".subtask")
	t.Setenv("SUBTASK_DIR", subtaskDir)
	addStubCommandToPATH(t, "codex")
	cwd := t.TempDir()

	require.NoError(t, os.MkdirAll(subtaskDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subtaskDir, "config.json"), []byte(`{"harness":"codex","max_workspaces":1}`+"\n"), 0o644))

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(settingsPath), 0o755))
	require.NoError(t, os.WriteFile(settingsPath, []byte(`{"enabledPlugins":{"subtask@subtask":true}}`+"\n"), 0o644))

	_ = runSubtask(t, bin, cwd, home, "install", "--no-prompt")

	var settings map[string]any
	require.NoError(t, readJSON(settingsPath, &settings))
	enabled, ok := settings["enabledPlugins"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, enabled["subtask@subtask"])
}

func TestInstall_Migration_PreservesComplexSettings(t *testing.T) {
	bin := buildSubtask(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	subtaskDir := filepath.Join(home, ".subtask")
	t.Setenv("SUBTASK_DIR", subtaskDir)
	addStubCommandToPATH(t, "codex")
	cwd := t.TempDir()

	const settingsJSON = `{
  "$schema": "https://json.schemastore.org/claude-code-settings.json",
  "alwaysThinkingEnabled": true,
  "enabledPlugins": {
    "rust-analyzer-lsp@claude-plugins-official": true,
    "gopls-lsp@claude-plugins-official": true,
    "dev-browser@dev-browser-marketplace": true,
    "subtask": true
  },
  "env": {
    "BASH_MAX_TIMEOUT_MS": "7200000"
  },
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "command": "echo 'hello'",
            "type": "command"
          }
        ],
        "matcher": "compact"
      }
    ]
  },
  "statusLine": {
    "command": "~/.claude/statusline.sh",
    "type": "command"
  }
}
`

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(settingsPath), 0o755))
	require.NoError(t, os.WriteFile(settingsPath, []byte(settingsJSON), 0o644))

	require.NoError(t, os.MkdirAll(subtaskDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subtaskDir, "config.json"), []byte(`{"harness":"codex","max_workspaces":1}`+"\n"), 0o644))

	_ = runSubtask(t, bin, cwd, home, "install", "--no-prompt")

	var got map[string]any
	require.NoError(t, readJSON(settingsPath, &got))

	enabled, ok := got["enabledPlugins"].(map[string]any)
	require.True(t, ok)
	require.Nil(t, enabled["subtask"])

	var expected map[string]any
	require.NoError(t, json.Unmarshal([]byte(settingsJSON), &expected))
	expectedEnabled, ok := expected["enabledPlugins"].(map[string]any)
	require.True(t, ok)
	delete(expectedEnabled, "subtask")
	expected["enabledPlugins"] = expectedEnabled

	require.Equal(t, expected, got)
}

func TestInstall_Migration_RunOnce_SkipsOnSecondInstall(t *testing.T) {
	bin := buildSubtask(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	subtaskDir := filepath.Join(home, ".subtask")
	t.Setenv("SUBTASK_DIR", subtaskDir)
	addStubCommandToPATH(t, "codex")
	cwd := t.TempDir()

	require.NoError(t, os.MkdirAll(subtaskDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subtaskDir, "config.json"), []byte(`{"harness":"codex","max_workspaces":1}`+"\n"), 0o644))

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(settingsPath), 0o755))
	require.NoError(t, os.WriteFile(settingsPath, []byte(`{"enabledPlugins":{"subtask":true,"other":true}}`+"\n"), 0o644))

	_ = runSubtask(t, bin, cwd, home, "install", "--no-prompt")

	var settings map[string]any
	require.NoError(t, readJSON(settingsPath, &settings))
	enabled, ok := settings["enabledPlugins"].(map[string]any)
	require.True(t, ok)
	require.Nil(t, enabled["subtask"])
	require.Equal(t, true, enabled["other"])

	markerPath := filepath.Join(subtaskDir, "migrations", "legacy-claude-plugin-v1.done")
	require.FileExists(t, markerPath)

	// Reintroduce the legacy key; second install should not run migration again.
	enabled["subtask"] = true
	settings["enabledPlugins"] = enabled
	b, err := json.MarshalIndent(settings, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(settingsPath, append(b, '\n'), 0o644))

	_ = runSubtask(t, bin, cwd, home, "install", "--no-prompt")

	require.NoError(t, readJSON(settingsPath, &settings))
	enabled, ok = settings["enabledPlugins"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, enabled["subtask"])
	require.Equal(t, true, enabled["other"])
}

func TestInstall_Migration_BothDirAndSettings(t *testing.T) {
	bin := buildSubtask(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	t.Setenv("SUBTASK_DIR", filepath.Join(home, ".subtask"))
	addStubCommandToPATH(t, "codex")
	cwd := t.TempDir()

	legacyDir := filepath.Join(home, ".claude", "plugins", "subtask")
	require.NoError(t, os.MkdirAll(legacyDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "sentinel"), []byte("x"), 0o644))

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(settingsPath), 0o755))
	require.NoError(t, os.WriteFile(settingsPath, []byte(`{"enabledPlugins":{"subtask":true,"other":true}}`+"\n"), 0o644))

	_ = runSubtask(t, bin, cwd, home, "install", "--no-prompt")

	_, err := os.Stat(legacyDir)
	require.ErrorIs(t, err, os.ErrNotExist)

	var settings map[string]any
	require.NoError(t, readJSON(settingsPath, &settings))
	enabled, ok := settings["enabledPlugins"].(map[string]any)
	require.True(t, ok)
	require.Nil(t, enabled["subtask"])
	require.Equal(t, true, enabled["other"])
}

func TestInstall_Migration_MalformedSettingsJSON_SkipsAndWarns(t *testing.T) {
	bin := buildSubtask(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	t.Setenv("SUBTASK_DIR", filepath.Join(home, ".subtask"))
	addStubCommandToPATH(t, "codex")
	cwd := t.TempDir()

	legacyDir := filepath.Join(home, ".claude", "plugins", "subtask")
	require.NoError(t, os.MkdirAll(legacyDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "sentinel"), []byte("x"), 0o644))

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(settingsPath), 0o755))
	require.NoError(t, os.WriteFile(settingsPath, []byte("{not json"), 0o644))

	out := runSubtask(t, bin, cwd, home, "install", "--no-prompt")
	require.Contains(t, out, "Skipped legacy settings cleanup")

	// Plugin dir removed even if settings.json was malformed.
	_, err := os.Stat(legacyDir)
	require.ErrorIs(t, err, os.ErrNotExist)

	// settings.json is untouched.
	data, err := os.ReadFile(settingsPath)
	require.NoError(t, err)
	require.Equal(t, "{not json", string(data))
}

func TestInstall_Guide_DoesNotWriteAnything(t *testing.T) {
	bin := buildSubtask(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	t.Setenv("SUBTASK_DIR", filepath.Join(home, ".subtask"))
	cwd := t.TempDir()

	out := runSubtask(t, bin, cwd, home, "install", "--guide")
	require.Contains(t, out, "# Setup Subtask")
	require.Contains(t, out, "Not in a git repository")

	// Debug
	entries, _ := os.ReadDir(home)
	t.Logf("home dir contents: %v", entries)
	if st := filepath.Join(home, ".subtask"); fileExists(st) {
		sub, _ := os.ReadDir(st)
		t.Logf(".subtask contents: %v", sub)
	}
	t.Logf("SUBTASK_DEBUG in test env: %s", os.Getenv("SUBTASK_DEBUG"))

	_, err := os.Stat(filepath.Join(home, ".claude"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(home, ".subtask"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestInstall_Guide_InGitRepo_MultipleHarnesses_ShowsHarnessChoice(t *testing.T) {
	bin := buildSubtask(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	t.Setenv("SUBTASK_DIR", filepath.Join(home, ".subtask"))

	addStubCommandToPATH(t, "codex")
	addStubCommandToPATH(t, "claude")

	repo := t.TempDir()
	initGitRepo(t, repo)

	out := runSubtask(t, bin, repo, home, "install", "--guide")
	require.Contains(t, out, "In a git repository")
	require.Contains(t, out, "Ask the user which harness")
	require.Contains(t, out, "subtask install --no-prompt --harness <codex|claude|opencode>")

	_, err := os.Stat(filepath.Join(home, ".subtask"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(home, ".claude"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestInstall_NoPrompt_Flags_WriteConfig(t *testing.T) {
	bin := buildSubtask(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	t.Setenv("SUBTASK_DIR", filepath.Join(home, ".subtask"))
	addStubCommandToPATH(t, "claude")

	cwd := t.TempDir()
	out := runSubtask(t, bin, cwd, home, "install", "--no-prompt", "--harness", "claude", "--model", "claude-sonnet-4-20250514", "--max-workspaces", "7")
	require.Contains(t, out, "Configured subtask")

	var cfg workspace.Config
	require.NoError(t, readJSON(filepath.Join(home, ".subtask", "config.json"), &cfg))
	require.Equal(t, "claude", cfg.Harness)
	require.Equal(t, 7, cfg.MaxWorkspaces)
	require.NotNil(t, cfg.Options)
	require.Equal(t, "claude-sonnet-4-20250514", cfg.Options["model"])
	_, hasReasoning := cfg.Options["reasoning"]
	require.False(t, hasReasoning)
}

func TestInstall_NoPrompt_ReasoningRequiresCodex(t *testing.T) {
	bin := buildSubtask(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	t.Setenv("SUBTASK_DIR", filepath.Join(home, ".subtask"))
	addStubCommandToPATH(t, "claude")

	cwd := t.TempDir()
	out, err := runSubtaskWithHomeEnv(t, bin, cwd, home, "install", "--no-prompt", "--harness", "claude", "--reasoning", "high")
	require.Error(t, err)
	require.Contains(t, out, "reasoning is codex-only")
}

func TestInstall_NoPrompt_InvalidHarnessRejected(t *testing.T) {
	bin := buildSubtask(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	t.Setenv("SUBTASK_DIR", filepath.Join(home, ".subtask"))
	addStubCommandToPATH(t, "codex")

	cwd := t.TempDir()
	out, err := runSubtaskWithHomeEnv(t, bin, cwd, home, "install", "--no-prompt", "--harness", "nope")
	require.Error(t, err)
	require.Contains(t, out, "invalid harness")
}

func TestInstall_ProjectScope_InstallsSkillToRepoOnly(t *testing.T) {
	bin := buildSubtask(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	t.Setenv("SUBTASK_DIR", filepath.Join(home, ".subtask"))
	addStubCommandToPATH(t, "codex")

	repo := t.TempDir()
	initGitRepo(t, repo)

	out := runSubtask(t, bin, repo, home, "install", "--no-prompt", "--scope", "project")
	require.Contains(t, out, "Installed skill")

	// Skill path should be project-scoped.
	projectSkillPath := filepath.Join(repo, ".claude", "skills", "subtask", "SKILL.md")
	gotSkill, err := os.ReadFile(projectSkillPath)
	require.NoError(t, err)
	require.Equal(t, install.Embedded(), gotSkill)

	// User-scope path should not be touched.
	_, err = os.Stat(filepath.Join(home, ".claude", "skills", "subtask", "SKILL.md"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestInstall_ProjectScope_RequiresGitRepo(t *testing.T) {
	bin := buildSubtask(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	t.Setenv("SUBTASK_DIR", filepath.Join(home, ".subtask"))
	addStubCommandToPATH(t, "codex")

	cwd := t.TempDir()
	out, err := runSubtaskWithHomeEnv(t, bin, cwd, home, "install", "--no-prompt", "--scope", "project")
	require.Error(t, err)
	require.Contains(t, out, "--scope=project requires being in a git repository")
}

func TestAutoUpdate_RepairsDriftOnlyWhenInstalled(t *testing.T) {
	bin := buildSubtask(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	t.Setenv("SUBTASK_DIR", filepath.Join(home, ".subtask"))
	addStubCommandToPATH(t, "codex")
	cwd := t.TempDir()

	// Not installed: running any command should not create files.
	_ = runSubtask(t, bin, cwd, home, "--version")
	_, err := os.Stat(filepath.Join(home, ".claude", "skills", "subtask", "SKILL.md"))
	require.ErrorIs(t, err, os.ErrNotExist)

	// Install, then drift, then run status to trigger auto-update.
	_ = runSubtask(t, bin, cwd, home, "install", "--no-prompt")
	skillPath := filepath.Join(home, ".claude", "skills", "subtask", "SKILL.md")
	require.NoError(t, os.WriteFile(skillPath, []byte("different"), 0o644))

	out := runSubtask(t, bin, cwd, home, "status")
	require.Contains(t, out, "Updated skill to latest version")

	gotSkill, err := os.ReadFile(skillPath)
	require.NoError(t, err)
	require.Equal(t, install.Embedded(), gotSkill)
}

func runSubtask(t *testing.T, bin string, dir string, home string, args ...string) string {
	t.Helper()
	out, err := runSubtaskWithHomeEnv(t, bin, dir, home, args...)
	require.NoError(t, err, "%s", out)
	return out
}

func runSubtaskWithHomeEnv(t *testing.T, bin string, dir string, home string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	env := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if len(kv) >= 5 && kv[:5] == "HOME=" {
			continue
		}
		if len(kv) >= 12 && kv[:12] == "USERPROFILE=" {
			continue
		}
		// Filter out debug env var so tests run with predictable logging behavior.
		if strings.HasPrefix(kv, "SUBTASK_DEBUG=") {
			t.Logf("filtering out SUBTASK_DEBUG: %q", kv)
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"HOME="+home,
		"USERPROFILE="+home, // windows
	)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run(t, dir, "git", "init", "-b", "main")
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test User")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o644))
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "Initial commit")
}

func TestInstallCLI_UsesWindowsExeName(t *testing.T) {
	// Guard: buildSubtask() already handles windows suffix; keep this to ensure
	// the helper stays correct if modified.
	if runtime.GOOS != "windows" {
		t.Skip("windows-only")
	}
	bin := buildSubtask(t)
	require.Contains(t, filepath.Base(bin), ".exe")
}
