package workspace

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/zippoxer/subtask/pkg/task"
)

var validReasoningLevels = []string{"low", "medium", "high", "xhigh"}

func ValidateReasoningLevel(reasoning string) error {
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return nil
	}
	if slices.Contains(validReasoningLevels, reasoning) {
		return nil
	}
	return fmt.Errorf("invalid reasoning %q\n\nAllowed: %s", reasoning, strings.Join(validReasoningLevels, ", "))
}

func ValidateReasoningFlag(harnessName, reasoning string) error {
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return nil
	}
	if strings.TrimSpace(harnessName) != "codex" {
		return fmt.Errorf("reasoning is codex-only\n\nRemove --reasoning, or switch your harness to codex with:\n  subtask config --user\nor (repo-only):\n  subtask config --project")
	}
	return ValidateReasoningLevel(reasoning)
}

func ResolveModel(cfg *Config, t *task.Task, override string) string {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override)
	}
	if t != nil && strings.TrimSpace(t.Model) != "" {
		return strings.TrimSpace(t.Model)
	}
	if cfg != nil && cfg.Options != nil {
		if s, ok := cfg.Options["model"].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func ResolveReasoning(cfg *Config, t *task.Task, override string) string {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override)
	}
	if t != nil && strings.TrimSpace(t.Reasoning) != "" {
		return strings.TrimSpace(t.Reasoning)
	}
	if cfg != nil && cfg.Options != nil {
		if s, ok := cfg.Options["reasoning"].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func ConfigWithModelReasoning(cfg *Config, model, reasoning string) *Config {
	if cfg == nil {
		return nil
	}
	cp := *cfg
	cp.Options = maps.Clone(cfg.Options)
	if cp.Options == nil {
		cp.Options = make(map[string]any)
	}

	model = strings.TrimSpace(model)
	if model == "" {
		delete(cp.Options, "model")
	} else {
		cp.Options["model"] = model
	}

	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		delete(cp.Options, "reasoning")
	} else {
		cp.Options["reasoning"] = reasoning
	}

	return &cp
}
