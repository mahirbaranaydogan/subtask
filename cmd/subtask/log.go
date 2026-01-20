package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zippoxer/subtask/pkg/task/history"
	"github.com/zippoxer/subtask/pkg/task/migrate"
)

// LogCmd implements 'subtask log' (conversation + lifecycle history).
type LogCmd struct {
	Task     string `arg:"" help:"Task name"`
	Events   bool   `help:"Show lifecycle events only"`
	Messages bool   `help:"Show messages only"`
	Since    string `help:"Show entries since duration or timestamp (e.g., '5m', '1h', '2024-01-01T10:00:00Z')"`
}

func (c *LogCmd) Run() error {
	if c.Events && c.Messages {
		return fmt.Errorf("--events and --messages are mutually exclusive")
	}

	if _, err := preflightProject(); err != nil {
		return err
	}

	if err := migrate.EnsureSchema(c.Task); err != nil {
		return err
	}

	var since time.Time
	if strings.TrimSpace(c.Since) != "" {
		t, err := parseSince(c.Since)
		if err != nil {
			return fmt.Errorf("invalid --since value: %w", err)
		}
		since = t
	}

	evs, err := history.Read(c.Task, history.ReadOptions{
		Since:        since,
		MessagesOnly: c.Messages,
		EventsOnly:   c.Events,
	})
	if err != nil {
		return err
	}
	if len(evs) == 0 {
		return nil
	}

	for _, ev := range evs {
		fmt.Println(formatHistoryEvent(ev))
	}
	return nil
}

func formatHistoryEvent(ev history.Event) string {
	ts := ""
	if !ev.TS.IsZero() {
		ts = ev.TS.Local().Format(time.RFC3339)
	}

	if ev.Type == "message" {
		role := strings.TrimSpace(ev.Role)
		if role == "" {
			role = "unknown"
		}
		content := strings.TrimRight(ev.Content, "\n")
		if strings.TrimSpace(content) == "" {
			content = "(empty)"
		}
		if ts != "" {
			return fmt.Sprintf("%s %s: %s", ts, role, content)
		}
		return fmt.Sprintf("%s: %s", role, content)
	}

	// Lifecycle / runtime events
	desc := strings.TrimSpace(ev.Type)
	switch ev.Type {
	case "task.opened":
		var d struct {
			Reason     string `json:"reason"`
			From       string `json:"from"`
			Title      string `json:"title"`
			BaseBranch string `json:"base_branch"`
			Workflow   string `json:"workflow"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		if d.Reason != "" {
			desc = fmt.Sprintf("task opened (%s)", d.Reason)
			if d.Reason == "reopen" && d.From != "" {
				desc += fmt.Sprintf(" from %s", d.From)
			}
		}
		if d.BaseBranch != "" {
			desc += fmt.Sprintf(" base=%s", d.BaseBranch)
		}
		if d.Workflow != "" {
			desc += fmt.Sprintf(" workflow=%s", d.Workflow)
		}
	case "stage.changed":
		var d struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		if d.From != "" || d.To != "" {
			desc = fmt.Sprintf("stage changed: %s → %s", d.From, d.To)
		}
	case "task.merged":
		var d struct {
			Commit string `json:"commit"`
			Into   string `json:"into"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		if d.Commit != "" || d.Into != "" {
			desc = fmt.Sprintf("merged %s into %s", shortSHA(d.Commit), d.Into)
		}
	case "task.closed":
		var d struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		if d.Reason != "" {
			desc = fmt.Sprintf("task closed (%s)", d.Reason)
		} else {
			desc = "task closed"
		}
	case "worker.started":
		var d struct {
			RunID     string `json:"run_id"`
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		desc = "worker started"
		if d.RunID != "" {
			desc += " run=" + d.RunID
		}
		if d.SessionID != "" {
			desc += " session=" + d.SessionID
		}
	case "worker.finished":
		var d struct {
			RunID        string `json:"run_id"`
			DurationMS   int    `json:"duration_ms"`
			ToolCalls    int    `json:"tool_calls"`
			Outcome      string `json:"outcome"`
			ErrorMessage string `json:"error_message"`
			Error        string `json:"error"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		desc = "worker finished"
		if d.RunID != "" {
			desc += " run=" + d.RunID
		}
		if d.Outcome != "" {
			desc += " outcome=" + d.Outcome
		}
		if strings.TrimSpace(d.ErrorMessage) == "" {
			d.ErrorMessage = d.Error
		}
		if d.Outcome == "error" && strings.TrimSpace(d.ErrorMessage) != "" {
			desc += fmt.Sprintf(" error=%q", strings.TrimSpace(d.ErrorMessage))
		}
		if d.DurationMS > 0 {
			desc += " duration=" + (time.Duration(d.DurationMS) * time.Millisecond).String()
		}
		if d.ToolCalls > 0 {
			desc += fmt.Sprintf(" tool_calls=%d", d.ToolCalls)
		}
	}

	if ts != "" {
		return fmt.Sprintf("%s [%s]", ts, desc)
	}
	return fmt.Sprintf("[%s]", desc)
}

func shortSHA(sha string) string {
	s := strings.TrimSpace(sha)
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
