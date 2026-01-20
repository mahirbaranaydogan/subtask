package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zippoxer/subtask/pkg/render"
	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/gather"
	"github.com/zippoxer/subtask/pkg/task/history"
)

// ShowCmd implements 'subtask show'.
type ShowCmd struct {
	Task  string `arg:"" help:"Task name to show"`
	Watch bool   `short:"w" help:"Refresh every 2s (TTY only)"`
	JSON  bool   `short:"j" help:"Output as JSON"`
}

// Run executes the show command.
func (c *ShowCmd) Run() error {
	if _, err := preflightProject(); err != nil {
		return err
	}
	if c.JSON {
		if c.Watch {
			return fmt.Errorf("--watch cannot be used with --json")
		}
		out, err := c.renderJSON()
		if err != nil {
			return err
		}
		fmt.Print(out)
		return nil
	}

	if c.Watch {
		return runWatch(c.render)
	}

	out, err := c.render()
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

func (c *ShowCmd) render() (string, error) {
	detail, err := gather.Detail(context.Background(), c.Task)
	if err != nil {
		return "", err
	}

	t := detail.Task
	state := detail.State

	// Build task card.
	card := &render.TaskCard{
		Name:       t.Name,
		Title:      t.Title,
		Branch:     t.Name,
		BaseBranch: t.BaseBranch,
	}
	card.Model = detail.Model
	card.Reasoning = detail.Reasoning

	lastError := ""
	if state != nil {
		lastError = state.LastError
	}
	card.TaskStatus = userStatusTextWithIntegration(detail.TaskStatus, detail.WorkerStatus, time.Time{}, detail.LastRunMS, lastError, detail.IntegratedReason)
	if state != nil && detail.WorkerStatus == task.WorkerStatusRunning && !state.StartedAt.IsZero() {
		card.TaskStatus = userStatusTextWithIntegration(detail.TaskStatus, detail.WorkerStatus, state.StartedAt, detail.LastRunMS, lastError, detail.IntegratedReason)
	}

	if state != nil {
		card.Workspace = state.Workspace
		if detail.WorkerStatus == task.WorkerStatusError && strings.TrimSpace(state.LastError) != "" {
			card.Error = state.LastError
		}
	}

	card.LinesAdded = detail.LinesAdded
	card.LinesRemoved = detail.LinesRemoved
	card.CommitsBehind = detail.CommitsBehind
	card.ConflictFiles = detail.ConflictFiles

	// Workflow and stage if present.
	if detail.Workflow != nil {
		card.Workflow = detail.Workflow.Name
		if strings.TrimSpace(detail.Stage) != "" {
			card.Stage = render.FormatStageProgression(detail.Workflow.StageNames(), detail.Stage)
		}
	}

	// Load progress steps.
	card.ProgressSteps = make([]render.ProgressStep, 0, len(detail.ProgressSteps))
	for _, s := range detail.ProgressSteps {
		card.ProgressSteps = append(card.ProgressSteps, render.ProgressStep{Step: s.Step, Done: s.Done})
	}
	if len(card.ProgressSteps) > 0 {
		card.Progress = "" // Don't show summary when we have steps.
	}

	card.TaskDir = task.Dir(c.Task)
	card.Files = detail.TaskFiles

	if render.Pretty {
		return card.RenderPretty(), nil
	}
	return card.RenderPlain(), nil
}

type showJSONProgressStep struct {
	Step string `json:"step"`
	Done bool   `json:"done"`
}

type showJSON struct {
	Name             string                 `json:"name"`
	Title            string                 `json:"title,omitempty"`
	Branch           string                 `json:"branch,omitempty"`
	BaseBranch       string                 `json:"base_branch,omitempty"`
	Model            string                 `json:"model,omitempty"`
	Reasoning        string                 `json:"reasoning,omitempty"`
	Status           string                 `json:"status,omitempty"`
	WorkerStatus     string                 `json:"worker_status,omitempty"`
	Error            string                 `json:"error,omitempty"`
	Workspace        string                 `json:"workspace,omitempty"`
	Workflow         string                 `json:"workflow,omitempty"`
	Stage            string                 `json:"stage,omitempty"`
	TaskDir          string                 `json:"task_dir,omitempty"`
	Files            []string               `json:"files,omitempty"`
	ProgressSteps    []showJSONProgressStep `json:"progress_steps,omitempty"`
	LinesAdded       int                    `json:"lines_added,omitempty"`
	LinesRemoved     int                    `json:"lines_removed,omitempty"`
	CommitsBehind    int                    `json:"commits_behind,omitempty"`
	ConflictFiles    []string               `json:"conflict_files,omitempty"`
	IntegratedReason string                 `json:"integrated_reason,omitempty"`
	HistoryPath      string                 `json:"history_path,omitempty"`
	LastWorkerReply  string                 `json:"last_worker_reply,omitempty"`
}

func (c *ShowCmd) renderJSON() (string, error) {
	detail, err := gather.Detail(context.Background(), c.Task)
	if err != nil {
		return "", err
	}

	t := detail.Task
	state := detail.State

	out := showJSON{
		Name:             t.Name,
		Title:            t.Title,
		Branch:           t.Name,
		BaseBranch:       t.BaseBranch,
		Model:            detail.Model,
		Reasoning:        detail.Reasoning,
		HistoryPath:      task.HistoryPath(c.Task),
		LastWorkerReply:  lastWorkerReply(c.Task),
		TaskDir:          task.Dir(c.Task),
		Files:            detail.TaskFiles,
		LinesAdded:       detail.LinesAdded,
		LinesRemoved:     detail.LinesRemoved,
		CommitsBehind:    detail.CommitsBehind,
		ConflictFiles:    detail.ConflictFiles,
		IntegratedReason: detail.IntegratedReason,
	}

	out.Status = string(detail.TaskStatus)
	out.WorkerStatus = string(detail.WorkerStatus)
	out.Stage = detail.Stage
	if state != nil {
		out.Workspace = state.Workspace
		if detail.WorkerStatus == task.WorkerStatusError && state.LastError != "" {
			out.Error = state.LastError
		}
	}

	if detail.Workflow != nil {
		out.Workflow = detail.Workflow.Name
	}

	if steps := detail.ProgressSteps; len(steps) > 0 {
		out.ProgressSteps = make([]showJSONProgressStep, len(steps))
		for i, s := range steps {
			out.ProgressSteps[i] = showJSONProgressStep{Step: s.Step, Done: s.Done}
		}
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

func lastWorkerReply(taskName string) string {
	events, err := history.Read(taskName, history.ReadOptions{MessagesOnly: true})
	if err != nil || len(events) == 0 {
		return ""
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != "message" || strings.TrimSpace(events[i].Role) != "worker" {
			continue
		}
		return strings.TrimSpace(events[i].Content)
	}
	return ""
}
