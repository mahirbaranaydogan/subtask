package main

import (
	"context"
	"fmt"

	taskindex "github.com/zippoxer/subtask/pkg/task/index"
	"github.com/zippoxer/subtask/pkg/task/ops"
)

// MergeCmd implements 'subtask merge'.
type MergeCmd struct {
	Task    string `arg:"" help:"Task name to merge"`
	Message string `short:"m" required:"" help:"Commit message for the squash commit"`
}

// Run executes the merge command.
func (c *MergeCmd) Run() error {
	if _, err := preflightProject(); err != nil {
		return err
	}

	res, err := ops.MergeTask(c.Task, c.Message, cliOpsLogger{})
	if err != nil {
		return err
	}
	// Best-effort: refresh integration snapshot so list doesn't need a repair pass
	// after a subtask-driven merge advances the base branch.
	if idx, err := taskindex.OpenDefault(); err == nil {
		defer idx.Close()
		if err := idx.Refresh(context.Background(), taskindex.RefreshPolicy{
			Git: taskindex.GitPolicy{
				Mode:               taskindex.GitTasks,
				Tasks:              []string{c.Task},
				IncludeIntegration: true,
			},
		}); err != nil {
			printWarning(fmt.Sprintf("failed to refresh git integration cache: %v", err))
		}
	} else {
		printWarning(fmt.Sprintf("failed to open index for git integration cache refresh: %v", err))
	}
	if res.AlreadyClosed {
		if res.AlreadyMerged {
			fmt.Printf("Task %s is already merged.\n", c.Task)
		} else {
			fmt.Printf("Task %s is already closed (not merged).\n", c.Task)
		}
	}
	return nil
}
