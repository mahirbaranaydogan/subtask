package main

import (
	"fmt"
	"os"
	"text/template"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/zippoxer/subtask/internal/homedir"
	"github.com/zippoxer/subtask/pkg/install"
	"github.com/zippoxer/subtask/pkg/task"
)

// InstallCmd implements 'subtask install'.
type InstallCmd struct {
	Guide         bool   `help:"Print setup guidance and exit"`
	NoPrompt      bool   `help:"Non-interactive; use defaults"`
	Scope         string `help:"Skill scope: 'user' or 'project'" placeholder:"SCOPE"`
	Harness       string `help:"Worker harness: 'codex', 'claude', or 'opencode'" placeholder:"HARNESS"`
	Model         string `help:"Default model for workers" placeholder:"MODEL"`
	Reasoning     string `help:"Reasoning level for Codex: 'low', 'medium', 'high', 'xhigh'" placeholder:"LEVEL"`
	MaxWorkspaces int    `help:"Max parallel git worktrees per repo (default 20)" placeholder:"N"`
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

	// Determine scope - from flag, interactive, or default.
	// Project scope only makes sense inside a git repository.
	inGitRepo := isInGitRepo()
	scope := c.Scope
	if scope != "" && scope != "user" && scope != "project" {
		return fmt.Errorf("--scope must be 'user' or 'project', got %q", scope)
	}
	if scope == "project" && !inGitRepo {
		return fmt.Errorf("--scope=project requires being in a git repository")
	}
	if scope == "" {
		if c.NoPrompt || !inGitRepo {
			scope = "user"
		} else {
			var err error
			scope, err = runScopeWizard()
			if err != nil {
				return err
			}
		}
	}

	// Install skill to appropriate location.
	var skillPath string
	var updated bool
	if scope == "project" {
		repoRoot := task.ProjectRoot()
		skillPath, updated, err = install.InstallToProject(repoRoot)
	} else {
		skillPath, updated, err = install.InstallTo(homeDir)
	}
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
			WritePath:     task.ConfigPath(),
			Existing:      readConfigFileOrNil(task.ConfigPath()),
			NoPrompt:      c.NoPrompt,
			Harness:       c.Harness,
			Model:         c.Model,
			Reasoning:     c.Reasoning,
			MaxWorkspaces: c.MaxWorkspaces,
		})
		if err != nil {
			return err
		}
		if cfg != nil {
			printSuccess("Configured subtask")
			printConfigDetails(cfg, "user", task.ConfigPath())
		}
	} else if !updated {
		// Skill was already up to date and config exists - let user know how to reconfigure.
		fmt.Println()
		fmt.Println("Subtask is already installed. To change configuration:")
		fmt.Println("  subtask config        # edit global defaults")
		fmt.Println("  subtask config --project  # edit project overrides")
	}

	return nil
}

func printSetupGuide() {
	type guideData struct {
		InGitRepo           bool
		CodexAvailable      bool
		ClaudeAvailable     bool
		OpencodeAvailable   bool
		AnyHarnessAvailable bool
		MultipleHarnesses   bool
	}

	data := guideData{
		InGitRepo:         isInGitRepo(),
		CodexAvailable:    isCommandAvailable("codex"),
		ClaudeAvailable:   isCommandAvailable("claude"),
		OpencodeAvailable: isCommandAvailable("opencode"),
	}
	count := 0
	if data.CodexAvailable {
		count++
	}
	if data.ClaudeAvailable {
		count++
	}
	if data.OpencodeAvailable {
		count++
	}
	data.AnyHarnessAvailable = count > 0
	data.MultipleHarnesses = count > 1

	const tpl = `# Setup Subtask

**You (Claude Code) are the lead.** Subtask lets you create tasks, spawn subagents, track progress, review their work, and request changes. Each task runs in its own git worktree so they can work in parallel safely. The user doesn't run subtask commands — you do.

## Environment

{{if .InGitRepo}}✓ In a git repository{{else}}⚠ Not in a git repository (you'll need one later to create tasks){{end}}

**Available worker harnesses:**
{{if .CodexAvailable}}- ✓ Codex CLI (recommended)
{{else}}- ✗ Codex CLI not found — install from https://github.com/openai/codex
{{end}}{{if .ClaudeAvailable}}- ✓ Claude Code CLI
{{else}}- ✗ Claude Code CLI not found — install from https://claude.ai/download
{{end}}{{if .OpencodeAvailable}}- ✓ OpenCode CLI
{{else}}- ✗ OpenCode CLI not found — install from https://github.com/sst/opencode
{{end}}
{{if not .AnyHarnessAvailable}}**No harness available.** Install at least one (Codex recommended) before proceeding.
{{end}}
## Install
{{if .AnyHarnessAvailable}}
{{if .MultipleHarnesses}}Ask the user which harness they'd like to use for workers. Then run:

` + "```bash" + `
subtask install --no-prompt --harness <codex|claude|opencode>
` + "```" + `
{{else}}Run the install command:

` + "```bash" + `
subtask install --no-prompt
` + "```" + `
{{end}}
The install:
1. Installs the Subtask skill to ~/.claude/skills/subtask/
2. Writes config to ~/.subtask/config.json (with sensible defaults for model, etc.)

The user can change harness, model, or other settings later with ` + "`subtask config`" + `.
{{else}}
First install a worker harness, then run:

` + "```bash" + `
subtask install --no-prompt
` + "```" + `
{{end}}
## Ready
{{if not .InGitRepo}}
**Before creating tasks:** You're not in a git repository. If this looks like a project directory, offer to run ` + "`git init`" + `. Otherwise, ask the user where their project is.
{{end}}After install, load the Subtask skill with ` + "`/subtask`" + ` to get the full workflow instructions.

Then suggest example tasks adapted to the project, like:
- "Fix the login bug with Subtask"
- "Run these 3 features in parallel"
- "Plan and implement the new API endpoint with Subtask"

Once you start your first task, let the user know they can run ` + "`subtask`" + ` in a separate terminal to watch progress in the TUI.`

	t := template.Must(template.New("guide").Parse(tpl))
	if err := t.Execute(os.Stdout, data); err != nil {
		fmt.Fprintf(os.Stderr, "template error: %v\n", err)
	}
}

func isInGitRepo() bool {
	root, err := task.GitRootAbs()
	return err == nil && root != ""
}

func runScopeWizard() (string, error) {
	scope := "user"

	// Clear screen and show header.
	fmt.Print("\033[H\033[2J")
	fmt.Println()
	fmt.Println("  " + successStyle.Bold(true).Render("Install Claude Code Skill"))
	fmt.Println(subtleStyle.Render("  The skill teaches Claude Code the subtask commands and workflow"))
	fmt.Println()

	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Where to install the Claude Skill?").
			Options(
				huh.NewOption("Globally (recommended)", "user"),
				huh.NewOption("This project only", "project"),
			).
			Value(&scope),
	))

	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("esc", "ctrl+c"), key.WithHelp("esc", "cancel"))
	km.Select.Filter = key.NewBinding(key.WithDisabled())
	form = form.WithKeyMap(km).WithTheme(huh.ThemeCharm()).WithShowHelp(true)

	if err := form.Run(); err != nil {
		if err == huh.ErrUserAborted {
			return "", fmt.Errorf("install cancelled")
		}
		return "", err
	}

	return scope, nil
}
