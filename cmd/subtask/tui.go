package main

import (
	"fmt"
	"os"

	"github.com/zippoxer/subtask/pkg/logging"
	subtasktui "github.com/zippoxer/subtask/pkg/tui"
)

func runTUIWithInitCheck() error {
	if _, err := preflightProject(); err != nil {
		return err
	}

	if logging.DebugEnabled() {
		_, _ = fmt.Fprintf(os.Stderr, "SUBTASK_DEBUG enabled, writing logs to: %s\n", logging.LogPath())
	}

	return subtasktui.Run()
}
