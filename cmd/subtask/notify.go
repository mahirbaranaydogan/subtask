package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
)

const notifyPollInterval = 2 * time.Second

// NotifyCmd implements 'subtask notify'.
type NotifyCmd struct {
	Watch bool `short:"w" help:"Keep watching and notify when workers finish"`
}

type notifyState map[string]string

type workerFinishedData struct {
	RunID        string `json:"run_id"`
	DurationMS   int    `json:"duration_ms"`
	ToolCalls    int    `json:"tool_calls"`
	Outcome      string `json:"outcome"`
	Error        string `json:"error"`
	ErrorMessage string `json:"error_message"`
}

type finishedEvent struct {
	Task string
	Key  string
	Data workerFinishedData
}

// Run executes the notify command.
func (c *NotifyCmd) Run() error {
	if _, err := preflightProject(); err != nil {
		return err
	}

	if !c.Watch {
		count, err := notifyOnce()
		if err != nil {
			return err
		}
		if count == 0 {
			fmt.Println("No new worker replies.")
			return nil
		}
		fmt.Printf("Sent %d notification(s).\n", count)
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Println("Watching Subtask worker replies. Press Ctrl+C to stop.")
	for {
		if _, err := notifyOnce(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(notifyPollInterval):
		}
	}
}

func notifyOnce() (int, error) {
	state, err := loadNotifyState()
	if err != nil {
		return 0, err
	}

	events, err := latestFinishedEvents()
	if err != nil {
		return 0, err
	}

	sent := 0
	for _, ev := range events {
		if ev.Key == "" || state[ev.Task] == ev.Key {
			continue
		}
		notifyWorkerFinished(workerNotification{
			Task:      ev.Task,
			Outcome:   ev.Data.Outcome,
			Error:     firstNonEmpty(ev.Data.ErrorMessage, ev.Data.Error),
			Duration:  time.Duration(ev.Data.DurationMS) * time.Millisecond,
			ToolCalls: ev.Data.ToolCalls,
		})
		state[ev.Task] = ev.Key
		sent++
	}

	if sent > 0 {
		if err := saveNotifyState(state); err != nil {
			return sent, err
		}
	}
	return sent, nil
}

func latestFinishedEvents() ([]finishedEvent, error) {
	names, err := task.List()
	if err != nil {
		return nil, err
	}

	var out []finishedEvent
	for _, name := range names {
		ev, ok, err := latestFinishedEvent(name)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

func latestFinishedEvent(taskName string) (finishedEvent, bool, error) {
	events, err := history.Read(taskName, history.ReadOptions{EventsOnly: true})
	if err != nil {
		return finishedEvent{}, false, err
	}

	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Type != "worker.finished" {
			continue
		}
		var data workerFinishedData
		if len(ev.Data) > 0 {
			_ = json.Unmarshal(ev.Data, &data)
		}
		if strings.TrimSpace(data.Outcome) == "" {
			continue
		}
		key := strings.TrimSpace(data.RunID)
		if key == "" {
			key = ev.TS.Format(time.RFC3339Nano) + ":" + data.Outcome
		}
		return finishedEvent{Task: taskName, Key: key, Data: data}, true, nil
	}

	return finishedEvent{}, false, nil
}

func notifyStatePath() string {
	return filepath.Join(task.InternalDir(), "notifications.json")
}

func loadNotifyState() (notifyState, error) {
	path := notifyStatePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return notifyState{}, nil
		}
		return nil, err
	}
	var state notifyState
	if err := json.Unmarshal(data, &state); err != nil {
		return notifyState{}, nil
	}
	if state == nil {
		state = notifyState{}
	}
	return state, nil
}

func saveNotifyState(state notifyState) error {
	path := notifyStatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
