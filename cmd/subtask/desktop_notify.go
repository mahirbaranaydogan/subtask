package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/zippoxer/subtask/pkg/render"
)

type workerNotification struct {
	Task      string
	Outcome   string
	Error     string
	Duration  time.Duration
	ToolCalls int
}

func notifyWorkerFinished(n workerNotification) {
	if !desktopNotificationsEnabled() {
		return
	}

	title := "Subtask worker replied"
	if n.Outcome == "error" {
		title = "Subtask worker error"
	}

	parts := []string{n.Task}
	if n.Duration > 0 {
		parts = append(parts, render.FormatDuration(n.Duration))
	}
	if n.ToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d tool calls", n.ToolCalls))
	}
	if strings.TrimSpace(n.Error) != "" {
		parts = append(parts, strings.TrimSpace(n.Error))
	}

	_ = sendDesktopNotification(title, strings.Join(parts, " - "), "subtask-"+safeNotificationGroup(n.Task))
}

func desktopNotificationsEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("SUBTASK_NOTIFY")))
	if v == "0" || v == "false" || v == "off" || v == "no" {
		return false
	}
	if strings.HasSuffix(os.Args[0], ".test") && strings.TrimSpace(os.Getenv("SUBTASK_NOTIFY_FORCE")) == "" {
		return false
	}
	return true
}

func sendDesktopNotification(title, message, group string) error {
	if strings.TrimSpace(title) == "" || strings.TrimSpace(message) == "" {
		return nil
	}

	if runtime.GOOS == "darwin" {
		if path, err := exec.LookPath("terminal-notifier"); err == nil {
			return exec.Command(path,
				"-title", "Subtask",
				"-subtitle", title,
				"-message", message,
				"-group", group,
				"-activate", "com.openai.codex",
				"-sender", "com.openai.codex",
				"-sound", "default",
			).Run()
		}
		if path, err := exec.LookPath("osascript"); err == nil {
			script := fmt.Sprintf(
				"display notification %q with title %q subtitle %q",
				message,
				"Subtask",
				title,
			)
			return exec.Command(path, "-e", script).Run()
		}
		return nil
	}

	if runtime.GOOS == "linux" {
		if path, err := exec.LookPath("notify-send"); err == nil {
			return exec.Command(path, title, message).Run()
		}
		return nil
	}

	return nil
}

func safeNotificationGroup(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "worker"
	}
	replacer := strings.NewReplacer(
		string(filepath.Separator), "-",
		" ", "-",
		"\t", "-",
		"\n", "-",
		"\r", "-",
		":", "-",
	)
	return replacer.Replace(s)
}
