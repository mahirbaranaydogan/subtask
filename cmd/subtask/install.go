package main

import (
	"fmt"
	"os"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/zippoxer/subtask/pkg/install"
	"github.com/zippoxer/subtask/pkg/task"
)

// InstallCmd implements 'subtask install'.
type InstallCmd struct {
	Skill    bool   `help:"Install only the skill"`
	Plugin   bool   `help:"Install only the plugin"`
	Scope    string `default:"user" enum:"user,project" help:"Installation scope"`
	NoPrompt bool   `help:"Non-interactive; use defaults"`
}

func (c *InstallCmd) Run() error {
	scope, err := parseInstallScope(c.Scope)
	if err != nil {
		return err
	}

	installSkill := c.Skill
	installPlugin := c.Plugin
	if !c.Skill && !c.Plugin {
		installSkill = true
		installPlugin = true
	}

	if !c.NoPrompt && !c.Skill && !c.Plugin {
		installSkill = true
		installPlugin = true
		scope = install.ScopeUser
		if c.Scope != "" {
			if s, err := parseInstallScope(c.Scope); err == nil {
				scope = s
			}
		}

		baseDir, _, err := baseDirForScope(scope)
		if err != nil {
			return err
		}

		// Enter alternate screen buffer (preserves terminal history)
		fmt.Print("\033[?1049h")

		step := 0
		for {
			// Clear screen and show progress
			fmt.Print("\033[H\033[2J")
			fmt.Println()
			fmt.Println("  Install Subtask skill and Claude plugin")
			fmt.Println()
			if step > 0 {
				fmt.Printf("  Skill:  %s\n", yesNo(installSkill))
			}
			if step > 1 {
				fmt.Printf("  Plugin: %s\n", yesNo(installPlugin))
			}
			if step > 2 {
				fmt.Printf("  Scope:  %s\n", scope)
			}
			if step > 0 {
				fmt.Println()
			}

			var form *huh.Form
			switch step {
			case 0:
				form = huh.NewForm(huh.NewGroup(
					huh.NewConfirm().
						Title("Install skill?").
						Value(&installSkill),
				))
			case 1:
				form = huh.NewForm(huh.NewGroup(
					huh.NewConfirm().
						Title("Install plugin?").
						Value(&installPlugin),
				))
			case 2:
				form = huh.NewForm(huh.NewGroup(
					huh.NewSelect[install.Scope]().
						Title("Scope").
						Options(
							huh.NewOption("User (recommended)", install.ScopeUser),
							huh.NewOption("Project", install.ScopeProject),
						).
						Value(&scope),
				))
			default:
				goto done
			}

			km := huh.NewDefaultKeyMap()
			km.Quit = key.NewBinding(key.WithKeys("esc", "ctrl+c"), key.WithHelp("esc", "back"))
			km.Select.Filter = key.NewBinding(key.WithDisabled())
			form = form.WithKeyMap(km).WithTheme(huh.ThemeCharm()).WithShowHelp(true)

			if err := form.Run(); err == huh.ErrUserAborted {
				if step == 0 {
					fmt.Print("\033[?1049l") // exit alternate buffer
					return fmt.Errorf("install cancelled")
				}
				step--
				continue
			} else if err != nil {
				// Non-interactive; keep defaults and continue without prompting.
				break
			}

			// Recompute base dir if scope changes.
			if step == 2 {
				baseDir, _, err = baseDirForScope(scope)
				if err != nil {
					fmt.Print("\033[?1049l") // exit alternate buffer
					return err
				}
				_ = baseDir
			}

			step++
		}
	done:
		// Exit alternate screen buffer
		fmt.Print("\033[?1049l")
	}

	baseDir, inGit, err := baseDirForScope(scope)
	if err != nil {
		return err
	}

	res, err := install.InstallAll(install.InstallRequest{
		Scope:   scope,
		BaseDir: baseDir,
		Skill:   installSkill,
		Plugin:  installPlugin,
	})
	if err != nil {
		return err
	}

	if installSkill {
		msg := fmt.Sprintf("Installed skill to %s", abbreviatePath(res.SkillPath))
		if !res.UpdatedSkill {
			msg = fmt.Sprintf("Skill already up to date at %s", abbreviatePath(res.SkillPath))
		}
		printSuccess(msg)
	}

	if installPlugin {
		msg := fmt.Sprintf("Installed plugin to %s", abbreviatePath(res.PluginDir))
		if !res.UpdatedPlugin {
			msg = fmt.Sprintf("Plugin already up to date at %s", abbreviatePath(res.PluginDir))
		}
		printSuccess(msg)
		if res.Settings.Rewrote && res.Settings.BackupTo != "" {
			printWarning(fmt.Sprintf("Rewrote malformed settings.json (backup at %s)", abbreviatePath(res.Settings.BackupTo)))
		}
	}

	// If fully installed and not configured yet, run the config wizard and write ~/.subtask/config.json.
	if installSkill && installPlugin {
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
	}

	// Best-effort: ignore portable subtask data in git repos.
	if inGit {
		_ = ensureGitignore(baseDir)
	}

	return nil
}
