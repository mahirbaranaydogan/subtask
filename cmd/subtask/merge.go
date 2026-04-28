package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
	"github.com/zippoxer/subtask/pkg/task/ops"
	"github.com/zippoxer/subtask/pkg/workflow"
)

// MergeCmd implements 'subtask merge'.
type MergeCmd struct {
	Task    string `arg:"" help:"Task name to merge"`
	Message string `short:"m" required:"" help:"Commit message for the squash commit"`
	Force   bool   `help:"Merge even when workflow stage is not ready"`
}

// Run executes the merge command.
func (c *MergeCmd) Run() error {
	if _, err := preflightProject(); err != nil {
		return err
	}
	if bridgeNoMergeEnabled() {
		return fmt.Errorf("subtask merge is disabled inside a Codex bridge wakeup resume; review the task and ask the user to merge from a visible lead session")
	}
	if !c.Force {
		if err := requireReadyToMerge(c.Task); err != nil {
			return err
		}
	}

	res, err := ops.MergeTask(c.Task, c.Message, cliOpsLogger{})
	if err != nil {
		return err
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

func bridgeNoMergeEnabled() bool {
	return truthyEnv("SUBTASK_BRIDGE_NO_MERGE") || codexBridgeActiveResumeBlocksMerge(time.Now().UTC())
}

func requireReadyToMerge(taskName string) error {
	wf, err := workflow.LoadFromTask(taskName)
	if err != nil {
		return err
	}
	if wf == nil || len(wf.Stages) == 0 {
		return nil
	}

	tail, err := history.Tail(taskName)
	if err != nil {
		return err
	}
	if tail.TaskStatus != "" && tail.TaskStatus != task.TaskStatusOpen {
		return nil
	}

	finalStage := wf.Stages[len(wf.Stages)-1].Name
	currentStage := strings.TrimSpace(tail.Stage)
	if currentStage == "" {
		currentStage = wf.FirstStage()
	}
	if currentStage == finalStage {
		return nil
	}

	return fmt.Errorf("task %s is not ready to merge (stage: %s; required: %s)\n\n"+
		"Review and advance the workflow first:\n"+
		"  subtask stage %s %s\n"+
		"  subtask merge %s -m \"...\"\n\n"+
		"To override intentionally:\n"+
		"  subtask merge %s -m \"...\" --force",
		taskName, currentStage, finalStage, taskName, finalStage, taskName, taskName)
}
