package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	// Defaults (prefill from existing when possible).
	numWorkspaces := workspace.DefaultMaxWorkspaces
	h := "codex"
	model := "gpt-5.2"
	reasoning := "xhigh"

	if p.Existing != nil {
		if p.Existing.MaxWorkspaces > 0 {
			numWorkspaces = p.Existing.MaxWorkspaces
		}
		if strings.TrimSpace(p.Existing.Harness) != "" {
			h = strings.TrimSpace(p.Existing.Harness)
		}
		if m, ok := p.Existing.Options["model"].(string); ok && strings.TrimSpace(m) != "" {
			model = strings.TrimSpace(m)
		}
		if r, ok := p.Existing.Options["reasoning"].(string); ok && strings.TrimSpace(r) != "" {
			reasoning = strings.TrimSpace(r)
		}
	}

	// Normalize harness defaults to what's installed.
	if !isCommandAvailable(h) {
		switch {
		case codexAvailable:
			h = "codex"
		case claudeAvailable:
			h = "claude"
		default:
			h = "opencode"
		}
	}
	if h == "claude" && strings.TrimSpace(model) == "" {
		model = "claude-opus-4-5-20251101"
	}
	if h == "opencode" {
		reasoning = ""
	}
	if h != "codex" {
		reasoning = ""
	}

	if p.NoPrompt {
		cfg := &workspace.Config{
			Harness:       h,
			MaxWorkspaces: numWorkspaces,
			Options:       make(map[string]any),
		}
		if strings.TrimSpace(model) != "" {
			cfg.Options["model"] = model
		}
		if strings.TrimSpace(reasoning) != "" {
			cfg.Options["reasoning"] = reasoning
		}
		if err := cfg.SaveTo(p.WritePath); err != nil {
			return nil, false, fmt.Errorf("failed to save config: %w", err)
		}
		_ = harness.CanResolveCLI(cfg.Harness) // warm discovery
		return cfg, true, nil
	}

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
						Options(opts...).
						Value(&model),
				))
			} else if h == "claude" {
				opts := []huh.Option[string]{
					huh.NewOption("Claude Opus (recommended)", "claude-opus-4-5-20251101"),
					huh.NewOption("Claude Sonnet", "claude-sonnet-4-20250514"),
				}
				form = huh.NewForm(huh.NewGroup(
					huh.NewSelect[string]().
						Title("Model").
						Options(opts...).
						Value(&model),
				))
			} else {
				form = huh.NewForm(huh.NewGroup(
					huh.NewInput().
						Title("Model (optional)").
						Description("Leave blank to use OpenCode defaults; use provider/model to override.").
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
					Options(
						huh.NewOption("Extra High (recommended)", "xhigh"),
						huh.NewOption("High", "high"),
						huh.NewOption("Medium", "medium"),
						huh.NewOption("Low", "low"),
					).
					Value(&reasoning),
			))

		case 3:
			form = huh.NewForm(huh.NewGroup(
				huh.NewSelect[int]().
					Title("Max workspaces").
					Options(
						huh.NewOption("5", 5),
						huh.NewOption("10", 10),
						huh.NewOption("20 (recommended)", 20),
						huh.NewOption("50", 50),
					).
					Value(&numWorkspaces),
			))
		}

		if step > 3 {
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
				reasoning = "xhigh"
			case "claude":
				model = "claude-opus-4-5-20251101"
				reasoning = ""
			default:
				model = ""
				reasoning = ""
			}
		}

		step++
	}

	// Final validation - ensure selected harness is available.
	if !harness.CanResolveCLI(h) {
		switch h {
		case "codex":
			return nil, false, fmt.Errorf("codex CLI not found\n\nInstall it from: https://github.com/openai/codex")
		case "claude":
			return nil, false, fmt.Errorf("claude CLI not found\n\nInstall it from: https://claude.com/claude-code")
		default:
			return nil, false, fmt.Errorf("opencode CLI not found\n\nInstall it from: https://github.com/anomalyco/opencode")
		}
	}

	cfg := &workspace.Config{
		Harness:       h,
		MaxWorkspaces: numWorkspaces,
		Options:       make(map[string]any),
	}
	if strings.TrimSpace(model) != "" {
		cfg.Options["model"] = strings.TrimSpace(model)
	}
	if strings.TrimSpace(reasoning) != "" {
		cfg.Options["reasoning"] = strings.TrimSpace(reasoning)
	}
	if cfg.MaxWorkspaces <= 0 {
		cfg.MaxWorkspaces = workspace.DefaultMaxWorkspaces
	}

	if err := os.MkdirAll(filepath.Dir(p.WritePath), 0o755); err != nil {
		return nil, false, fmt.Errorf("failed to create config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("failed to marshal config: %w", err)
	}
	if err := os.WriteFile(p.WritePath, data, 0o644); err != nil {
		return nil, false, fmt.Errorf("failed to save config: %w", err)
	}

	// Warm harness discovery for better UX on first run.
	_ = harness.CanResolveCLI(cfg.Harness)

	return cfg, true, nil
}

