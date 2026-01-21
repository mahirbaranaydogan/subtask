package main

import (
	"fmt"
	"os"

	"github.com/zippoxer/subtask/internal/homedir"
	"github.com/zippoxer/subtask/pkg/install"
	"github.com/zippoxer/subtask/pkg/task"
)

// InstallCmd implements 'subtask install'.
type InstallCmd struct {
	Guide    bool `help:"Print setup guidance and exit"`
	NoPrompt bool `help:"Non-interactive; use defaults"`
}

func (c *InstallCmd) Run() error {
	if c.Guide {
		printSetupGuide()
		return nil
	}

	homeDir, err := homedir.Dir()
	if err != nil {
		return err
	}

	once, err := install.RunLegacyClaudePluginMigrationOnce(homeDir)
	if err != nil {
		return err
	}
	if once.Ran && once.Migration.SkippedSettingsMalformed {
		printWarning(fmt.Sprintf("Skipped legacy settings cleanup (malformed JSON at %s)", abbreviatePath(once.Migration.SettingsPath)))
	}
	if once.Ran && (once.Migration.RemovedLegacyPluginDir || once.Migration.RemovedLegacySettingsKey) {
		printSuccess("Removed legacy Claude plugin install artifacts")
	}

	skillPath, updated, err := install.InstallTo(homeDir)
	if err != nil {
		return err
	}
	if updated {
		printSuccess(fmt.Sprintf("Installed skill to %s", abbreviatePath(skillPath)))
	} else {
		printSuccess(fmt.Sprintf("Skill already up to date at %s", abbreviatePath(skillPath)))
	}

	// If not configured yet, run the config wizard and write ~/.subtask/config.json.
	if _, err := os.Stat(task.ConfigPath()); os.IsNotExist(err) {
		cfg, _, err := runConfigWizard(configWizardParams{
			WritePath: task.ConfigPath(),
			Existing:  readConfigFileOrNil(task.ConfigPath()),
			NoPrompt:  c.NoPrompt,
		})
		if err != nil {
			return err
		}
		if cfg != nil {
			printSuccess("Configured subtask")
		}
	}

	return nil
}

func printSetupGuide() {
	fmt.Println("Subtask setup (Claude Code)")
	fmt.Println()
	fmt.Println("Install the Subtask skill:")
	fmt.Println("  subtask install")
	fmt.Println()
	fmt.Println("Optional: project overrides:")
	fmt.Println("  subtask config --project")
	fmt.Println()
	fmt.Println("Optional: install the Claude plugin (skill reminders):")
	fmt.Println("  /plugin marketplace add zippoxer/subtask")
	fmt.Println("  /plugin install subtask@subtask")
	fmt.Println()
	fmt.Println("Example usage:")
	fmt.Println(`  "fix the login bug with Subtask"`)
	fmt.Println(`  "run these 3 features in parallel"`)
	fmt.Println(`  "plan and implement the new API endpoint with Subtask"`)
}
