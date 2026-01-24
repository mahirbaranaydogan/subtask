package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/zippoxer/subtask/pkg/harness"
	"github.com/zippoxer/subtask/pkg/workspace"
)

type configWizardParams struct {
	WritePath string
	RepoRoot  string // optional; used only for display/help text
	Existing  *workspace.Config
	NoPrompt  bool
	// Flag overrides (take precedence over defaults and existing config).
	Harness       string
	Model         string
	Reasoning     string
	MaxWorkspaces int
}

type configFlags struct {
	Harness       string
	Model         string
	Reasoning     string
	MaxWorkspaces int
}

type configValues struct {
	Harness       string
	Model         string
	Reasoning     string
	MaxWorkspaces int
}

// resolveConfigValues merges defaults + existing config + CLI flags into a resolved set of values.
// It is a pure function (no IO) and does not check harness availability on the machine.
func resolveConfigValues(existing *workspace.Config, flags configFlags) configValues {
	values := configValues{
		Harness:       "codex",
		MaxWorkspaces: workspace.DefaultMaxWorkspaces,
	}

	if existing != nil {
		if strings.TrimSpace(existing.Harness) != "" {
			values.Harness = strings.TrimSpace(existing.Harness)
		}
		if existing.MaxWorkspaces > 0 {
			values.MaxWorkspaces = existing.MaxWorkspaces
		}
		if existing.Options != nil {
			if m := stringsTrimSpace(existing.Options["model"]); m != "" {
				values.Model = m
			}
			if r := stringsTrimSpace(existing.Options["reasoning"]); r != "" {
				values.Reasoning = r
			}
		}
	}

	// Harness override resets dependent values to harness-appropriate defaults (but still allows
	// explicit flags to override after).
	if strings.TrimSpace(flags.Harness) != "" {
		values.Harness = strings.TrimSpace(flags.Harness)
		values.Model = ""
		values.Reasoning = ""
	}
	if strings.TrimSpace(flags.Model) != "" {
		values.Model = strings.TrimSpace(flags.Model)
	}
	if strings.TrimSpace(flags.Reasoning) != "" {
		values.Reasoning = strings.TrimSpace(flags.Reasoning)
	}
	if flags.MaxWorkspaces > 0 {
		values.MaxWorkspaces = flags.MaxWorkspaces
	}

	// Harness-specific defaults (only when unset).
	switch strings.TrimSpace(values.Harness) {
	case "", "codex":
		values.Harness = "codex"
		if strings.TrimSpace(values.Model) == "" {
			values.Model = "gpt-5.2"
		}
		if strings.TrimSpace(values.Reasoning) == "" {
			values.Reasoning = "high"
		}
	case "claude":
		if strings.TrimSpace(values.Model) == "" {
			values.Model = "opus"
		}
		// If reasoning came from defaults/existing and the user didn't explicitly set it as a flag,
		// drop it for non-codex harnesses (keeps config files clean and matches prior behavior).
		if strings.TrimSpace(flags.Reasoning) == "" {
			values.Reasoning = ""
		}
	case "opencode":
		if strings.TrimSpace(flags.Reasoning) == "" {
			values.Reasoning = ""
		}
	}

	return values
}

// validateConfigValues validates resolved values without performing any IO.
func validateConfigValues(values configValues) error {
	harnessName := strings.TrimSpace(values.Harness)
	switch harnessName {
	case "codex", "claude", "opencode":
		// ok
	default:
		return fmt.Errorf("invalid harness %q\n\nAllowed: codex, claude, opencode", harnessName)
	}

	if values.MaxWorkspaces < 0 {
		return fmt.Errorf("max workspaces must be >= 0, got %d", values.MaxWorkspaces)
	}

	return workspace.ValidateReasoningFlag(harnessName, strings.TrimSpace(values.Reasoning))
}

// buildConfig creates a workspace.Config from resolved values.
func buildConfig(values configValues) *workspace.Config {
	cfg := &workspace.Config{
		Harness:       strings.TrimSpace(values.Harness),
		MaxWorkspaces: values.MaxWorkspaces,
	}
	if cfg.MaxWorkspaces <= 0 {
		cfg.MaxWorkspaces = workspace.DefaultMaxWorkspaces
	}

	model := strings.TrimSpace(values.Model)
	reasoning := strings.TrimSpace(values.Reasoning)
	if model != "" || reasoning != "" {
		cfg.Options = make(map[string]any)
		if model != "" {
			cfg.Options["model"] = model
		}
		if reasoning != "" {
			cfg.Options["reasoning"] = reasoning
		}
	}

	return cfg
}

func validateHarnessAvailable(harnessName string) error {
	if harness.CanResolveCLI(harnessName) {
		return nil
	}
	switch harnessName {
	case "codex":
		return fmt.Errorf("codex CLI not found\n\nInstall it from: https://github.com/openai/codex")
	case "claude":
		return fmt.Errorf("claude CLI not found\n\nInstall it from: https://claude.com/claude-code")
	default:
		return fmt.Errorf("opencode CLI not found\n\nInstall it from: https://github.com/anomalyco/opencode")
	}
}

func runConfigWizard(p configWizardParams) (*workspace.Config, bool, error) {
	if strings.TrimSpace(p.WritePath) == "" {
		return nil, false, fmt.Errorf("config write path is required")
	}

	// Check which harnesses are available.
	codexAvailable := isCommandAvailable("codex")
	claudeAvailable := isCommandAvailable("claude")
	opencodeAvailable := isCommandAvailable("opencode")
	if !codexAvailable && !claudeAvailable && !opencodeAvailable {
		return nil, false, fmt.Errorf("no worker harness available\n\nInstall one of:\n  - Codex CLI: https://github.com/openai/codex\n  - Claude Code CLI: https://claude.com/claude-code\n  - OpenCode CLI: https://github.com/anomalyco/opencode")
	}

	flags := configFlags{
		Harness:       p.Harness,
		Model:         p.Model,
		Reasoning:     p.Reasoning,
		MaxWorkspaces: p.MaxWorkspaces,
	}
	values := resolveConfigValues(p.Existing, flags)

	// If the user didn't explicitly request a harness and the resolved harness isn't available,
	// fall back to the first available harness and reset dependent defaults.
	if strings.TrimSpace(flags.Harness) == "" && !isCommandAvailable(values.Harness) {
		fallbackHarness := "opencode"
		switch {
		case codexAvailable:
			fallbackHarness = "codex"
		case claudeAvailable:
			fallbackHarness = "claude"
		}
		values = resolveConfigValues(nil, configFlags{
			Harness:       fallbackHarness,
			Model:         flags.Model,
			Reasoning:     flags.Reasoning,
			MaxWorkspaces: values.MaxWorkspaces,
		})
	}

	if p.NoPrompt {
		if err := validateConfigValues(values); err != nil {
			return nil, false, err
		}
		if err := validateHarnessAvailable(strings.TrimSpace(values.Harness)); err != nil {
			return nil, false, err
		}
		cfg := buildConfig(values)
		if err := cfg.SaveTo(p.WritePath); err != nil {
			return nil, false, fmt.Errorf("failed to save config: %w", err)
		}
		_ = harness.CanResolveCLI(cfg.Harness) // warm discovery
		return cfg, true, nil
	}

	// Use resolved defaults to prefill the wizard.
	h := values.Harness
	model := values.Model
	reasoning := values.Reasoning
	numWorkspaces := values.MaxWorkspaces

	// Interactive wizard (same flow as prior init).
	firstStep := 0
	available := 0
	if codexAvailable {
		available++
	}
	if claudeAvailable {
		available++
	}
	if opencodeAvailable {
		available++
	}
	if available <= 1 {
		firstStep = 1 // skip harness selection
	}

	step := firstStep
	for {
		// Clear screen and show header + previous answers.
		fmt.Print("\033[H\033[2J")
		fmt.Println()
		fmt.Println("  " + successStyle.Bold(true).Render("Subtask Config"))
		fmt.Println(subtleStyle.Render("  Configure parallel workers"))
		fmt.Println()

		if step > 0 && firstStep == 0 {
			fmt.Printf("  Harness:   %s\n", h)
		}
		if step > 1 && model != "" {
			fmt.Printf("  Model:     %s\n", model)
		}
		if step > 2 && h == "codex" {
			fmt.Printf("  Reasoning: %s\n", reasoning)
		}
		if step > firstStep {
			fmt.Println()
		}

		var form *huh.Form
		switch step {
		case 0:
			var opts []huh.Option[string]
			if codexAvailable {
				opts = append(opts, huh.NewOption("Codex (recommended)", "codex"))
			}
			if claudeAvailable {
				opts = append(opts, huh.NewOption("Claude Code", "claude"))
			}
			if opencodeAvailable {
				opts = append(opts, huh.NewOption("OpenCode", "opencode"))
			}
			form = huh.NewForm(huh.NewGroup(
				huh.NewSelect[string]().
					Title("Worker").
					Description("Which CLI runs your tasks behind the scenes").
					Options(opts...).
					Value(&h),
			))

		case 1:
			if h == "codex" {
				opts := []huh.Option[string]{
					huh.NewOption("gpt-5.2 (recommended)", "gpt-5.2"),
					huh.NewOption("gpt-5.2-codex", "gpt-5.2-codex"),
				}
				form = huh.NewForm(huh.NewGroup(
					huh.NewSelect[string]().
						Title("Model").
						Description("Default for workers. Change anytime with: subtask config").
						Options(opts...).
						Value(&model),
				))
			} else if h == "claude" {
				opts := []huh.Option[string]{
					huh.NewOption("Opus (recommended)", "opus"),
					huh.NewOption("Sonnet", "sonnet"),
				}
				form = huh.NewForm(huh.NewGroup(
					huh.NewSelect[string]().
						Title("Model").
						Description("Default for workers. Change anytime with: subtask config").
						Options(opts...).
						Value(&model),
				))
			} else {
				form = huh.NewForm(huh.NewGroup(
					huh.NewInput().
						Title("Model (optional)").
						Description("Default for workers. Leave blank for OpenCode defaults. Change anytime with: subtask config").
						Placeholder("provider/model").
						Value(&model),
				))
			}

		case 2:
			if h != "codex" {
				step++
				continue
			}
			form = huh.NewForm(huh.NewGroup(
				huh.NewSelect[string]().
					Title("Reasoning").
					Description("Default for workers. Change anytime with: subtask config").
					Options(
						huh.NewOption("Extra High", "xhigh"),
						huh.NewOption("High (recommended)", "high"),
						huh.NewOption("Medium", "medium"),
						huh.NewOption("Low", "low"),
					).
					Value(&reasoning),
			))
		}

		if step > 2 {
			break
		}

		km := huh.NewDefaultKeyMap()
		km.Quit = key.NewBinding(key.WithKeys("esc", "ctrl+c"), key.WithHelp("esc", "back"))
		km.Select.Filter = key.NewBinding(key.WithDisabled())
		form = form.WithKeyMap(km).WithTheme(huh.ThemeCharm()).WithShowHelp(true)

		err := form.Run()
		if err == huh.ErrUserAborted {
			if step == firstStep {
				return nil, false, fmt.Errorf("config cancelled")
			}
			step--
			if step == 2 && h != "codex" {
				step--
			}
			continue
		}
		if err != nil {
			return nil, false, err
		}

		// Reset dependent values when harness changes.
		if step == 0 {
			switch h {
			case "codex":
				model = "gpt-5.2"
				reasoning = "high"
			case "claude":
				model = "opus"
				reasoning = ""
			default:
				model = ""
				reasoning = ""
			}
		}

		step++
	}

	values = configValues{
		Harness:       h,
		Model:         model,
		Reasoning:     reasoning,
		MaxWorkspaces: numWorkspaces,
	}

	// Final validation - ensure selections are valid and harness is available.
	if err := validateConfigValues(values); err != nil {
		return nil, false, err
	}
	if err := validateHarnessAvailable(strings.TrimSpace(values.Harness)); err != nil {
		return nil, false, err
	}

	cfg := buildConfig(values)
	if err := cfg.SaveTo(p.WritePath); err != nil {
		return nil, false, fmt.Errorf("failed to save config: %w", err)
	}

	// Warm harness discovery for better UX on first run.
	_ = harness.CanResolveCLI(cfg.Harness)

	return cfg, true, nil
}
