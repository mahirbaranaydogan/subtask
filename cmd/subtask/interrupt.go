package main

import (
	"fmt"
	"time"

	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
	"github.com/zippoxer/subtask/pkg/task/migrate"
)

// InterruptCmd implements 'subtask interrupt' (alias: 'subtask stop').
type InterruptCmd struct {
	Task string `arg:"" help:"Task name"`
}

var interruptSignalFn = sendInterruptSignal

func (c *InterruptCmd) Run() error {
	if _, err := preflightProject(); err != nil {
		return err
	}

	// Ensure schema/history exist (one-time) and task exists.
	if err := migrate.EnsureSchema(c.Task); err != nil {
		return err
	}

	// Snapshot running run_id (best-effort; used for history correlation).
	tail, _ := history.Tail(c.Task)
	runID := tail.RunningRunID

	now := time.Now().UTC()

	var pid, pgid int
	if err := task.WithLock(c.Task, func() error {
		st, err := task.LoadState(c.Task)
		if err != nil {
			return err
		}
		if st == nil || st.SupervisorPID == 0 {
			return fmt.Errorf("task %s is not working", c.Task)
		}
		if st.IsStale() {
			st.SupervisorPID = 0
			st.SupervisorPGID = 0
			st.StartedAt = time.Time{}
			if st.LastError == "" {
				st.LastError = "supervisor process died"
			}
			_ = st.Save(c.Task)
			return fmt.Errorf("task %s is not working (stale supervisor pid cleared)", c.Task)
		}

		pid = st.SupervisorPID
		pgid = st.SupervisorPGID

		data := map[string]any{
			"action":         "requested",
			"run_id":         runID,
			"signal":         "SIGINT",
			"supervisor_pid": pid,
		}
		if pgid != 0 {
			data["supervisor_pgid"] = pgid
		}
		_ = history.AppendLocked(c.Task, history.Event{
			Type: "worker.interrupt",
			TS:   now,
			Data: mustJSON(data),
		})
		return nil
	}); err != nil {
		return err
	}

	if err := interruptSignalFn(pid, pgid); err != nil {
		if isNoSuchProcess(err) {
			_ = task.WithLock(c.Task, func() error {
				st, _ := task.LoadState(c.Task)
				if st == nil {
					return nil
				}
				if st.SupervisorPID == pid {
					st.SupervisorPID = 0
					st.SupervisorPGID = 0
					st.StartedAt = time.Time{}
					if st.LastError == "" {
						st.LastError = "supervisor process exited"
					}
					_ = st.Save(c.Task)
				}
				return nil
			})
			return fmt.Errorf("task %s is not working (supervisor process exited)", c.Task)
		}
		return fmt.Errorf("failed to interrupt task %s: %w", c.Task, err)
	}

	fmt.Printf("Sent SIGINT to %s.\n", c.Task)
	return nil
}
