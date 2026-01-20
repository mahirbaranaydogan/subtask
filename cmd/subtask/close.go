package main

import (
	"fmt"

	"github.com/zippoxer/subtask/pkg/task/ops"
)

// CloseCmd implements 'subtask close'.
type CloseCmd struct {
	Task    string `arg:"" help:"Task name to close"`
	Abandon bool   `help:"Discard uncommitted changes"`
}

// Run executes the close command.
func (c *CloseCmd) Run() error {
	if _, err := preflightProject(); err != nil {
		return err
	}

	res, err := ops.CloseTask(c.Task, c.Abandon, cliOpsLogger{})
	if err != nil {
		return err
	}
	if res.AlreadyClosed {
		fmt.Printf("Task %s is already closed.\n", c.Task)
		return nil
	}

	fmt.Printf("Closed %s. Workspace freed.\n", c.Task)
	return nil
}
