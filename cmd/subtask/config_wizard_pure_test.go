package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/workspace"
)

func TestResolveConfigValues_Defaults(t *testing.T) {
	values := resolveConfigValues(nil, configFlags{})
	require.Equal(t, "codex", values.Harness)
	require.Equal(t, "gpt-5.2", values.Model)
	require.Equal(t, "high", values.Reasoning)
	require.Equal(t, workspace.DefaultMaxWorkspaces, values.MaxWorkspaces)
}

func TestResolveConfigValues_ExistingClaude_DefaultsModel_DropsReasoning(t *testing.T) {
	existing := &workspace.Config{
		Harness:       "claude",
		MaxWorkspaces: 7,
		Options: map[string]any{
			"reasoning": "high",
		},
	}
	values := resolveConfigValues(existing, configFlags{})
	require.Equal(t, "claude", values.Harness)
	require.Equal(t, "opus", values.Model)
	require.Empty(t, values.Reasoning)
	require.Equal(t, 7, values.MaxWorkspaces)
}

func TestResolveConfigValues_FlagsHarnessOverride_ResetsDependentDefaults(t *testing.T) {
	existing := &workspace.Config{
		Harness: "codex",
		Options: map[string]any{
			"model":     "gpt-5.2-codex",
			"reasoning": "xhigh",
		},
	}
	values := resolveConfigValues(existing, configFlags{Harness: "claude"})
	require.Equal(t, "claude", values.Harness)
	require.Equal(t, "opus", values.Model)
	require.Empty(t, values.Reasoning)
}

func TestResolveConfigValues_FlagsOverrideModelAndReasoning(t *testing.T) {
	values := resolveConfigValues(nil, configFlags{
		Harness:   "codex",
		Model:     "gpt-5.2-codex",
		Reasoning: "medium",
	})
	require.Equal(t, "codex", values.Harness)
	require.Equal(t, "gpt-5.2-codex", values.Model)
	require.Equal(t, "medium", values.Reasoning)
}

func TestValidateConfigValues_InvalidHarness(t *testing.T) {
	err := validateConfigValues(configValues{Harness: "nope"})
	require.ErrorContains(t, err, "invalid harness")
}

func TestValidateConfigValues_ReasoningCodexOnly(t *testing.T) {
	err := validateConfigValues(configValues{Harness: "claude", Reasoning: "high"})
	require.ErrorContains(t, err, "codex-only")
}

func TestValidateConfigValues_MaxWorkspacesNegative(t *testing.T) {
	err := validateConfigValues(configValues{Harness: "codex", MaxWorkspaces: -1})
	require.ErrorContains(t, err, "max workspaces must be >= 0")
}

func TestBuildConfig_UsesDefaultsAndOmitsEmptyOptions(t *testing.T) {
	cfg := buildConfig(configValues{Harness: "codex", MaxWorkspaces: 0})
	require.Equal(t, "codex", cfg.Harness)
	require.Equal(t, workspace.DefaultMaxWorkspaces, cfg.MaxWorkspaces)
	require.Nil(t, cfg.Options)
}

func TestBuildConfig_SetsOptions(t *testing.T) {
	cfg := buildConfig(configValues{
		Harness:   "codex",
		Model:     "gpt-5.2-codex",
		Reasoning: "high",
	})
	require.Equal(t, "codex", cfg.Harness)
	require.Equal(t, "gpt-5.2-codex", cfg.Options["model"])
	require.Equal(t, "high", cfg.Options["reasoning"])
}
