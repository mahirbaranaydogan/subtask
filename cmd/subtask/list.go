package main

import (
	"context"
	"fmt"

	"github.com/zippoxer/subtask/pkg/task/gather"
)

// ListCmd implements 'subtask list'.
type ListCmd struct {
	All bool `short:"a" help:"Show all tasks including closed"`
}

// Run executes the list command.
func (c *ListCmd) Run() error {
	if _, err := preflightProject(); err != nil {
		return err
	}
	out, err := c.render()
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

func (c *ListCmd) render() (string, error) {
	data, err := gather.List(context.Background(), gather.ListOptions{All: c.All})
	if err != nil {
		return "", err
	}

	if len(data.Items) == 0 && len(data.Workspaces) == 0 {
		return "No tasks.\n", nil
	}

	tasks := make([]TaskInfo, 0, len(data.Items))
	for _, it := range data.Items {
		info := TaskInfo{
			Name:             it.Name,
			Title:            it.Title,
			FollowUp:         it.FollowUp,
			BaseBranch:       it.BaseBranch,
			TaskStatus:       it.TaskStatus,
			WorkerStatus:     it.WorkerStatus,
			Stage:            it.Stage,
			Workspace:        it.Workspace,
			StartedAt:        it.StartedAt,
			LastActive:       it.LastActive,
			ToolCalls:        it.ToolCalls,
			LinesAdded:       it.LinesAdded,
			LinesRemoved:     it.LinesRemoved,
			CommitsBehind:    it.CommitsBehind,
			LastRunMS:        it.LastRunDurationMS,
			LastError:        it.LastError,
			IntegratedReason: it.IntegratedReason,
		}
		if it.ProgressTotal > 0 {
			info.Progress = fmt.Sprintf("%d/%d", it.ProgressDone, it.ProgressTotal)
		}
		tasks = append(tasks, info)
	}

	return RenderTaskList(tasks, data.Workspaces), nil
}
