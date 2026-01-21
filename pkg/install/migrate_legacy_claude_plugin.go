package install

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/zippoxer/subtask/pkg/task"
)

type LegacyClaudePluginMigrationResult struct {
	RemovedLegacyPluginDir   bool
	RemovedLegacySettingsKey bool
	SkippedSettingsMalformed bool

	PluginDir    string
	SettingsPath string
}

type LegacyClaudePluginMigrationOnceResult struct {
	Ran        bool
	Migration  LegacyClaudePluginMigrationResult
	MarkerPath string
}

// MigrateLegacyClaudePluginInstall cleans up artifacts created by the old (broken) plugin installer.
//
// Conservative behavior:
// - Always best-effort delete ~/.claude/plugins/subtask (does not error if missing).
// - Only edits settings.json if it exists and contains enabledPlugins as an object containing {"subtask": true}.
// - If settings.json is malformed JSON, it is left untouched and SkippedSettingsMalformed is set.
func MigrateLegacyClaudePluginInstall(homeDir string) (LegacyClaudePluginMigrationResult, error) {
	res := LegacyClaudePluginMigrationResult{
		PluginDir:    filepath.Join(homeDir, ".claude", "plugins", "subtask"),
		SettingsPath: filepath.Join(homeDir, ".claude", "settings.json"),
	}

	// Best-effort delete legacy plugin dir.
	if homeDir != "" {
		if _, err := os.Stat(res.PluginDir); err == nil {
			if err := os.RemoveAll(res.PluginDir); err == nil {
				res.RemovedLegacyPluginDir = true
			}
		} else if os.IsNotExist(err) {
			// noop
		} else {
			// Unexpected stat error; ignore and proceed.
		}
	}

	// Settings: do not create, do not touch unless we can safely remove the legacy key.
	if homeDir == "" {
		return res, nil
	}
	if _, err := os.Stat(res.SettingsPath); err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		// If we can't stat settings, skip.
		return res, nil
	}

	data, err := os.ReadFile(res.SettingsPath)
	if err != nil {
		return res, nil
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		res.SkippedSettingsMalformed = true
		return res, nil
	}

	plugins, ok := m["enabledPlugins"].(map[string]any)
	if !ok || plugins == nil {
		return res, nil
	}

	legacyVal, ok := plugins["subtask"].(bool)
	if !ok || !legacyVal {
		return res, nil
	}

	delete(plugins, "subtask")
	m["enabledPlugins"] = plugins

	info, err := os.Stat(res.SettingsPath)
	if err != nil {
		return res, nil
	}

	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return res, nil
	}
	b = append(b, '\n')

	if err := writeFileAtomic(res.SettingsPath, b, info.Mode().Perm()); err != nil {
		return res, nil
	}

	res.RemovedLegacySettingsKey = true
	return res, nil
}

func RunLegacyClaudePluginMigrationOnce(homeDir string) (LegacyClaudePluginMigrationOnceResult, error) {
	res := LegacyClaudePluginMigrationOnceResult{
		MarkerPath: filepath.Join(task.GlobalDir(), "migrations", "legacy-claude-plugin-v1.done"),
	}

	if homeDir == "" {
		return res, nil
	}

	if err := os.MkdirAll(filepath.Dir(res.MarkerPath), 0o755); err != nil {
		return LegacyClaudePluginMigrationOnceResult{}, err
	}

	f, err := os.OpenFile(res.MarkerPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return res, nil
		}
		return LegacyClaudePluginMigrationOnceResult{}, err
	}
	_ = f.Close()

	legacyPluginDir := filepath.Join(homeDir, ".claude", "plugins", "subtask")
	shouldRun := fileExists(legacyPluginDir) || fileExists(task.ConfigPath())
	if !shouldRun {
		return res, nil
	}

	mig, err := MigrateLegacyClaudePluginInstall(homeDir)
	if err != nil {
		return LegacyClaudePluginMigrationOnceResult{}, err
	}
	res.Ran = true
	res.Migration = mig
	return res, nil
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		// Windows rename does not overwrite; fall back to remove+rename.
		_ = os.Remove(path)
		if err2 := os.Rename(tmpPath, path); err2 != nil {
			return err
		}
	}

	ok = true
	return nil
}
