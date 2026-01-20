package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zippoxer/subtask/pkg/render"
	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
	"github.com/zippoxer/subtask/pkg/task/migrate"
	"github.com/zippoxer/subtask/pkg/workflow"
)

// StageCmd implements 'subtask stage'.
type StageCmd struct {
	Task  string `arg:"" help:"Task name"`
	Stage string `arg:"" help:"Stage to set"`
}

// Run executes the stage command.
func (c *StageCmd) Run() error {
	if _, err := preflightProject(); err != nil {
		return err
	}

	if err := migrate.EnsureSchema(c.Task); err != nil {
		return err
	}

	// Load workflow from task folder
	wf, err := workflow.LoadFromTask(c.Task)
	if err != nil {
		return fmt.Errorf("failed to load workflow: %w", err)
	}
	if wf == nil {
		return fmt.Errorf("task %q has no workflow\n\nStage is only for tasks created with --workflow", c.Task)
	}

	// Validate stage exists
	if wf.StageIndex(c.Stage) < 0 {
		return fmt.Errorf("unknown stage %q\n\nValid stages: %s", c.Stage, strings.Join(wf.StageNames(), ", "))
	}

	var oldStage string
	if err := task.WithLock(c.Task, func() error {
		state, _ := task.LoadState(c.Task)
		if state != nil && state.SupervisorPID != 0 && !state.IsStale() {
			return fmt.Errorf("task %q is working\n\nWait for it to finish first", c.Task)
		}

		tail, _ := history.Tail(c.Task)
		oldStage = tail.Stage
		if oldStage == "" {
			oldStage = wf.FirstStage()
		}

		data, _ := json.Marshal(map[string]any{"from": oldStage, "to": c.Stage})
		return history.AppendLocked(c.Task, history.Event{TS: time.Now().UTC(), Type: "stage.changed", Data: data})
	}); err != nil {
		return err
	}

	// Print result
	if oldStage != "" && oldStage != c.Stage {
		printSuccess(fmt.Sprintf("%s: %s → %s", c.Task, oldStage, c.Stage))
	} else {
		printSuccess(fmt.Sprintf("%s: %s", c.Task, c.Stage))
	}

	// Print new stage guidance
	stage := wf.GetStage(c.Stage)
	if stage != nil && stage.Instructions != "" {
		fmt.Println()
		printStageGuidance(c.Task, wf, c.Stage)
	}

	return nil
}

// printStageGuidance prints the guidance for a stage.
func printStageGuidance(taskName string, wf *workflow.Workflow, stageName string) {
	stage := wf.GetStage(stageName)
	if stage == nil {
		return
	}

	// Print stage progression
	fmt.Printf("Stage: %s\n", render.FormatStageProgression(wf.StageNames(), stageName))
	fmt.Println()

	// Print stage name and guidance (capitalize first letter)
	displayName := stageName
	if len(displayName) > 0 {
		displayName = strings.ToUpper(displayName[:1]) + displayName[1:]
	}
	fmt.Printf("%s:\n", displayName)
	// Indent guidance
	lines := strings.Split(strings.TrimSpace(stage.Instructions), "\n")
	for _, line := range lines {
		// Replace <task> placeholder with actual task name
		line = strings.ReplaceAll(line, "<task>", taskName)
		fmt.Printf("  %s\n", line)
	}
}
