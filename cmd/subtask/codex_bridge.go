package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/zippoxer/subtask/internal/filelock"
	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/task/history"
)

const codexBridgePollInterval = 2 * time.Second

// CodexBridgeCmd implements 'subtask codex-bridge'.
type CodexBridgeCmd struct {
	Bind   CodexBridgeBindCmd   `cmd:"" help:"Bind tasks or task prefixes to a Codex lead session"`
	Unbind CodexBridgeUnbindCmd `cmd:"" help:"Remove Codex lead bindings"`
	List   CodexBridgeListCmd   `cmd:"" help:"List Codex lead bindings"`
	Ping   CodexBridgePingCmd   `cmd:"" help:"Inject a diagnostic wakeup into a visible Codex lead terminal"`
	Status CodexBridgeStatusCmd `cmd:"" help:"Show Codex lead bridge status"`
	Watch  CodexBridgeWatchCmd  `cmd:"" help:"Watch worker replies and notify or resume bound Codex leads"`
}

type CodexBridgeBindCmd struct {
	Lead       string `help:"Lead name" required:""`
	Session    string `help:"Codex session/thread id to resume" required:""`
	Task       string `help:"Exact task name to bind"`
	TaskPrefix string `name:"task-prefix" help:"Task name prefix to bind"`
	Delivery   string `help:"Delivery mode: notify, exec-resume, terminal-inject, or warp-launch" default:"notify"`
	TTY        string `name:"tty" help:"TTY path for terminal-inject delivery; auto-detects the visible codex resume session when omitted"`
	FromNow    bool   `name:"from-now" help:"Do not deliver worker replies that already existed before this binding"`
}

type CodexBridgeUnbindCmd struct {
	Lead       string `help:"Remove all bindings for this lead"`
	Task       string `help:"Exact task binding to remove"`
	TaskPrefix string `name:"task-prefix" help:"Task prefix binding to remove"`
}

type CodexBridgeListCmd struct{}

type CodexBridgeStatusCmd struct{}

type CodexBridgePingCmd struct {
	Lead     string `help:"Lead name to ping"`
	Session  string `help:"Codex session/thread id to ping"`
	TTY      string `name:"tty" help:"TTY path to inject into; auto-detects from session when omitted"`
	Delivery string `help:"Visible ping delivery mode: terminal-inject or warp-launch" default:"terminal-inject"`
	Message  string `help:"Message to inject" default:"Subtask bridge ping: visible Codex wakeup test. Reply briefly that the bridge ping arrived, then stop."`
}

type CodexBridgeWatchCmd struct {
	Once bool          `help:"Run one delivery pass and exit"`
	Poll time.Duration `help:"Polling interval for watch mode" default:"2s"`
}

type codexBridgeState struct {
	Bindings []codexLeadBinding `json:"bindings"`
}

type codexLeadBinding struct {
	Lead       string    `json:"lead"`
	SessionID  string    `json:"session_id"`
	Task       string    `json:"task,omitempty"`
	TaskPrefix string    `json:"task_prefix,omitempty"`
	Delivery   string    `json:"delivery,omitempty"`
	TTY        string    `json:"tty,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type codexBridgeDeliveries struct {
	Deliveries map[string]codexBridgeDelivery `json:"deliveries"`
}

type codexBridgeDelivery struct {
	Task      string    `json:"task"`
	RunID     string    `json:"run_id"`
	Lead      string    `json:"lead"`
	SessionID string    `json:"session_id"`
	Outcome   string    `json:"outcome"`
	Mode      string    `json:"mode,omitempty"`
	Delivered time.Time `json:"delivered_at"`
}

type codexBridgeMatch struct {
	Binding codexLeadBinding
	Matched bool
}

type codexBridgeResumeRequest struct {
	RepoRoot string
	Task     string
	Stage    string
	Event    finishedEvent
	Binding  codexLeadBinding
}

type codexBridgeActiveResume struct {
	Lead      string    `json:"lead"`
	SessionID string    `json:"session_id"`
	Task      string    `json:"task"`
	RunID     string    `json:"run_id"`
	PID       int       `json:"pid"`
	ExpiresAt time.Time `json:"expires_at"`
}

var runCodexBridgeResume = runCodexBridgeResumeCommand

const (
	codexBridgeDeliveryNotify         = "notify"
	codexBridgeDeliveryExecResume     = "exec-resume"
	codexBridgeDeliveryTerminalInject = "terminal-inject"
	codexBridgeDeliveryWarpLaunch     = "warp-launch"
)

func (c *CodexBridgeBindCmd) Run() error {
	if _, err := preflightProject(); err != nil {
		return err
	}
	binding, err := c.binding()
	if err != nil {
		return err
	}

	if err := withCodexBridgeStoreLock(func() error {
		state, err := loadCodexBridgeState()
		if err != nil {
			return err
		}
		if conflict := state.conflict(binding); conflict != nil {
			return conflict
		}
		state.upsert(binding)
		if err := saveCodexBridgeState(state); err != nil {
			return err
		}
		if c.FromNow {
			if err := markExistingFinishedEventsDeliveredLocked(binding); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	target := binding.Task
	if target == "" {
		target = binding.TaskPrefix + "*"
	}
	fmt.Printf("Bound %s to lead %s (%s) with %s delivery.\n", target, binding.Lead, binding.SessionID, binding.deliveryMode())
	return nil
}

func (c *CodexBridgeBindCmd) binding() (codexLeadBinding, error) {
	lead := strings.TrimSpace(c.Lead)
	session := strings.TrimSpace(c.Session)
	taskName := strings.TrimSpace(c.Task)
	prefix := strings.TrimSpace(c.TaskPrefix)
	delivery := strings.TrimSpace(c.Delivery)
	if delivery == "" {
		delivery = codexBridgeDeliveryNotify
	}
	if lead == "" {
		return codexLeadBinding{}, fmt.Errorf("--lead is required")
	}
	if session == "" {
		return codexLeadBinding{}, fmt.Errorf("--session is required")
	}
	if (taskName == "") == (prefix == "") {
		return codexLeadBinding{}, fmt.Errorf("provide exactly one of --task or --task-prefix")
	}
	if !validCodexBridgeDelivery(delivery) {
		return codexLeadBinding{}, fmt.Errorf("--delivery must be %q, %q, %q, or %q", codexBridgeDeliveryNotify, codexBridgeDeliveryExecResume, codexBridgeDeliveryTerminalInject, codexBridgeDeliveryWarpLaunch)
	}
	now := time.Now().UTC()
	return codexLeadBinding{
		Lead:       lead,
		SessionID:  session,
		Task:       taskName,
		TaskPrefix: prefix,
		Delivery:   delivery,
		TTY:        normalizeTTYPath(c.TTY),
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

func validCodexBridgeDelivery(delivery string) bool {
	switch strings.TrimSpace(delivery) {
	case codexBridgeDeliveryNotify, codexBridgeDeliveryExecResume, codexBridgeDeliveryTerminalInject, codexBridgeDeliveryWarpLaunch:
		return true
	default:
		return false
	}
}

func (s *codexBridgeState) upsert(next codexLeadBinding) {
	if s == nil {
		return
	}
	for i := range s.Bindings {
		if s.Bindings[i].sameTarget(next) {
			next.CreatedAt = s.Bindings[i].CreatedAt
			s.Bindings[i] = next
			sortCodexBindings(s.Bindings)
			return
		}
	}
	s.Bindings = append(s.Bindings, next)
	sortCodexBindings(s.Bindings)
}

func (s *codexBridgeState) conflict(next codexLeadBinding) error {
	if s == nil {
		return nil
	}
	for _, existing := range s.Bindings {
		if !existing.sameTarget(next) {
			continue
		}
		if existing.Lead == next.Lead && existing.SessionID == next.SessionID {
			return nil
		}
		target := next.Task
		if target == "" {
			target = next.TaskPrefix + "*"
		}
		return fmt.Errorf("%s is already bound to lead %s (%s)", target, existing.Lead, existing.SessionID)
	}
	return nil
}

func (b codexLeadBinding) sameTarget(other codexLeadBinding) bool {
	return b.Task == other.Task && b.TaskPrefix == other.TaskPrefix
}

func (b codexLeadBinding) deliveryMode() string {
	delivery := strings.TrimSpace(b.Delivery)
	if delivery == "" {
		return codexBridgeDeliveryNotify
	}
	return delivery
}

func (c *CodexBridgeUnbindCmd) Run() error {
	if _, err := preflightProject(); err != nil {
		return err
	}
	if strings.TrimSpace(c.Lead) == "" && strings.TrimSpace(c.Task) == "" && strings.TrimSpace(c.TaskPrefix) == "" {
		return fmt.Errorf("provide --lead, --task, or --task-prefix")
	}
	removed := 0
	if err := withCodexBridgeStoreLock(func() error {
		state, err := loadCodexBridgeState()
		if err != nil {
			return err
		}
		kept := state.Bindings[:0]
		for _, b := range state.Bindings {
			if c.matches(b) {
				removed++
				continue
			}
			kept = append(kept, b)
		}
		state.Bindings = kept
		return saveCodexBridgeState(state)
	}); err != nil {
		return err
	}
	fmt.Printf("Removed %d binding(s).\n", removed)
	return nil
}

func (c *CodexBridgeUnbindCmd) matches(b codexLeadBinding) bool {
	if lead := strings.TrimSpace(c.Lead); lead != "" && b.Lead != lead {
		return false
	}
	if taskName := strings.TrimSpace(c.Task); taskName != "" && b.Task != taskName {
		return false
	}
	if prefix := strings.TrimSpace(c.TaskPrefix); prefix != "" && b.TaskPrefix != prefix {
		return false
	}
	return true
}

func (c *CodexBridgeListCmd) Run() error {
	if _, err := preflightProject(); err != nil {
		return err
	}
	state, err := loadCodexBridgeState()
	if err != nil {
		return err
	}
	printCodexBridgeBindings(state)
	return nil
}

func (c *CodexBridgeStatusCmd) Run() error {
	if _, err := preflightProject(); err != nil {
		return err
	}
	state, err := loadCodexBridgeState()
	if err != nil {
		return err
	}
	deliveries, err := loadCodexBridgeDeliveries()
	if err != nil {
		return err
	}
	printCodexBridgeBindings(state)
	fmt.Println()
	fmt.Println("Deliveries:")
	if len(deliveries.Deliveries) == 0 {
		fmt.Println("  (none)")
		return nil
	}
	keys := make([]string, 0, len(deliveries.Deliveries))
	for k := range deliveries.Deliveries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		d := deliveries.Deliveries[k]
		mode := strings.TrimSpace(d.Mode)
		if mode == "" {
			mode = codexBridgeDeliveryNotify
		}
		fmt.Printf("  %s -> %s (%s, %s, %s)\n", d.Task, d.Lead, d.Outcome, mode, d.RunID)
	}
	return nil
}

func (c *CodexBridgePingCmd) Run() error {
	if _, err := preflightProject(); err != nil {
		return err
	}
	binding, err := c.resolveBinding()
	if err != nil {
		return err
	}
	message := strings.TrimSpace(c.Message)
	if message == "" {
		return fmt.Errorf("--message cannot be empty")
	}
	delivery := strings.TrimSpace(c.Delivery)
	if delivery == "" {
		delivery = codexBridgeDeliveryTerminalInject
	}
	req := codexBridgeResumeRequest{
		RepoRoot: mustProjectRootForPing(),
		Task:     "codex-bridge/ping",
		Stage:    "diagnostic",
		Event: finishedEvent{
			Task: "codex-bridge/ping",
			Key:  fmt.Sprintf("ping-%d", time.Now().UnixNano()),
			Data: workerFinishedData{Outcome: "replied"},
		},
		Binding: binding,
	}
	switch delivery {
	case codexBridgeDeliveryTerminalInject:
		ttyPath, err := resolveCodexLeadTTY(binding)
		if err != nil {
			return err
		}
		if err := injectTerminalInput(ttyPath, message+"\n"); err != nil {
			if errors.Is(err, os.ErrPermission) {
				fmt.Fprintf(os.Stderr, "subtask: warning: terminal inject denied by macOS, launching visible Warp fallback\n")
				return runCodexBridgeVisibleLaunchDelivery(req, message)
			}
			return err
		}
		fmt.Printf("Injected Codex bridge ping into %s for lead %s (%s).\n", ttyPath, binding.Lead, binding.SessionID)
		return nil
	case codexBridgeDeliveryWarpLaunch:
		return runCodexBridgeVisibleLaunchDelivery(req, message)
	default:
		return fmt.Errorf("--delivery must be %q or %q", codexBridgeDeliveryTerminalInject, codexBridgeDeliveryWarpLaunch)
	}
}

func mustProjectRootForPing() string {
	res, err := preflightProject()
	if err != nil {
		return "."
	}
	return res.RepoRoot
}

func runCodexBridgeVisibleLaunchDelivery(req codexBridgeResumeRequest, prompt string) error {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = buildCodexBridgeTerminalPrompt(req)
	}
	promptPath, launchPath, err := writeCodexBridgeWarpLaunchFiles(req, prompt)
	if err != nil {
		return err
	}
	if err := openWarpLaunchConfig(launchPath); err != nil {
		return err
	}
	fmt.Printf("Launched visible Warp wakeup for lead %s (%s) using %s and %s.\n", req.Binding.Lead, req.Binding.SessionID, launchPath, promptPath)
	return nil
}

func (c *CodexBridgePingCmd) resolveBinding() (codexLeadBinding, error) {
	session := strings.TrimSpace(c.Session)
	lead := strings.TrimSpace(c.Lead)
	ttyPath := normalizeTTYPath(c.TTY)
	if session != "" {
		return codexLeadBinding{
			Lead:      firstNonEmpty(lead, "manual"),
			SessionID: session,
			Delivery:  codexBridgeDeliveryTerminalInject,
			TTY:       ttyPath,
		}, nil
	}
	if lead == "" {
		return codexLeadBinding{}, fmt.Errorf("provide --lead or --session")
	}
	state, err := loadCodexBridgeState()
	if err != nil {
		return codexLeadBinding{}, err
	}
	for _, b := range state.Bindings {
		if b.Lead != lead {
			continue
		}
		if ttyPath != "" {
			b.TTY = ttyPath
		}
		return b, nil
	}
	return codexLeadBinding{}, fmt.Errorf("lead %s is not bound", lead)
}

func (c *CodexBridgeWatchCmd) Run() error {
	res, err := preflightProject()
	if err != nil {
		return err
	}
	interval := c.Poll
	if interval <= 0 {
		interval = codexBridgePollInterval
	}
	if c.Once {
		count, err := codexBridgeDeliverOnce(context.Background(), res.RepoRoot)
		if err != nil {
			return err
		}
		fmt.Printf("Handled %d Codex bridge event(s).\n", count)
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Println("Watching Subtask worker replies for Codex leads. Press Ctrl+C to stop.")
	for {
		if _, err := codexBridgeDeliverOnce(ctx, res.RepoRoot); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}

func codexBridgeDeliverOnce(ctx context.Context, repoRoot string) (int, error) {
	requests, err := codexBridgePendingDeliveries(repoRoot)
	if err != nil {
		return 0, err
	}
	delivered := 0
	for _, req := range requests {
		ok, err := withCodexLeadLock(req.Binding.Lead, func() error {
			return runCodexBridgeResume(ctx, req)
		})
		if err != nil {
			return delivered, err
		}
		if !ok {
			continue
		}
		if err := markCodexBridgeDelivered(req); err != nil {
			return delivered, err
		}
		delivered++
	}
	return delivered, nil
}

func codexBridgePendingDeliveries(repoRoot string) ([]codexBridgeResumeRequest, error) {
	var requests []codexBridgeResumeRequest
	err := withCodexBridgeStoreLock(func() error {
		var err error
		requests, err = codexBridgePendingDeliveriesLocked(repoRoot)
		return err
	})
	return requests, err
}

func codexBridgePendingDeliveriesLocked(repoRoot string) ([]codexBridgeResumeRequest, error) {
	state, err := loadCodexBridgeState()
	if err != nil {
		return nil, err
	}
	if len(state.Bindings) == 0 {
		return nil, nil
	}
	deliveries, err := loadCodexBridgeDeliveries()
	if err != nil {
		return nil, err
	}
	events, err := latestFinishedEvents()
	if err != nil {
		return nil, err
	}
	requests := make([]codexBridgeResumeRequest, 0)
	for _, ev := range events {
		if ev.Key == "" {
			continue
		}
		match := state.resolve(ev.Task)
		if !match.Matched {
			continue
		}
		deliveryID := codexDeliveryID(ev.Task, ev.Key)
		if _, ok := deliveries.Deliveries[deliveryID]; ok {
			continue
		}
		tail, err := history.Tail(ev.Task)
		if err != nil {
			return requests, err
		}
		requests = append(requests, codexBridgeResumeRequest{
			RepoRoot: repoRoot,
			Task:     ev.Task,
			Stage:    strings.TrimSpace(tail.Stage),
			Event:    ev,
			Binding:  match.Binding,
		})
	}
	return requests, nil
}

func markCodexBridgeDelivered(req codexBridgeResumeRequest) error {
	return withCodexBridgeStoreLock(func() error {
		deliveries, err := loadCodexBridgeDeliveries()
		if err != nil {
			return err
		}
		deliveryID := codexDeliveryID(req.Task, req.Event.Key)
		if _, ok := deliveries.Deliveries[deliveryID]; ok {
			return nil
		}
		deliveries.Deliveries[deliveryID] = codexBridgeDelivery{
			Task:      req.Task,
			RunID:     req.Event.Key,
			Lead:      req.Binding.Lead,
			SessionID: req.Binding.SessionID,
			Outcome:   req.Event.Data.Outcome,
			Mode:      req.Binding.deliveryMode(),
			Delivered: time.Now().UTC(),
		}
		return saveCodexBridgeDeliveries(deliveries)
	})
}

func markExistingFinishedEventsDelivered(binding codexLeadBinding) error {
	return withCodexBridgeStoreLock(func() error {
		return markExistingFinishedEventsDeliveredLocked(binding)
	})
}

func markExistingFinishedEventsDeliveredLocked(binding codexLeadBinding) error {
	deliveries, err := loadCodexBridgeDeliveries()
	if err != nil {
		return err
	}
	events, err := latestFinishedEvents()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	marked := false
	for _, ev := range events {
		if ev.Key == "" || !binding.matchesTask(ev.Task) {
			continue
		}
		id := codexDeliveryID(ev.Task, ev.Key)
		if _, exists := deliveries.Deliveries[id]; exists {
			continue
		}
		deliveries.Deliveries[id] = codexBridgeDelivery{
			Task:      ev.Task,
			RunID:     ev.Key,
			Lead:      binding.Lead,
			SessionID: binding.SessionID,
			Outcome:   ev.Data.Outcome,
			Mode:      binding.deliveryMode(),
			Delivered: now,
		}
		marked = true
	}
	if !marked {
		return nil
	}
	return saveCodexBridgeDeliveries(deliveries)
}

func (b codexLeadBinding) matchesTask(taskName string) bool {
	if strings.TrimSpace(b.Task) != "" {
		return b.Task == taskName
	}
	prefix := strings.TrimSpace(b.TaskPrefix)
	return prefix != "" && strings.HasPrefix(taskName, prefix)
}

func (s codexBridgeState) resolve(taskName string) codexBridgeMatch {
	for _, b := range s.Bindings {
		if strings.TrimSpace(b.Task) == taskName {
			return codexBridgeMatch{Binding: b, Matched: true}
		}
	}
	var best codexLeadBinding
	bestLen := -1
	for _, b := range s.Bindings {
		prefix := strings.TrimSpace(b.TaskPrefix)
		if prefix == "" || !strings.HasPrefix(taskName, prefix) {
			continue
		}
		if len(prefix) > bestLen {
			best = b
			bestLen = len(prefix)
		}
	}
	if bestLen >= 0 {
		return codexBridgeMatch{Binding: best, Matched: true}
	}
	return codexBridgeMatch{}
}

func runCodexBridgeResumeCommand(ctx context.Context, req codexBridgeResumeRequest) error {
	switch req.Binding.deliveryMode() {
	case codexBridgeDeliveryNotify:
		return runCodexBridgeNotifyDelivery(req)
	case codexBridgeDeliveryTerminalInject:
		return runCodexBridgeTerminalInjectDelivery(req)
	case codexBridgeDeliveryWarpLaunch:
		sendCodexBridgeDesktopNotification(req, "Opening visible Codex lead")
		return runCodexBridgeVisibleLaunchDelivery(req, buildCodexBridgeTerminalPrompt(req))
	}
	cleanupActive, err := markCodexBridgeActiveResume(req, 10*time.Minute)
	if err != nil {
		return err
	}
	defer cleanupActive()
	sendCodexBridgeDesktopNotification(req, "Waking Codex lead")
	prompt := buildCodexBridgePrompt(req)
	resumeCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	args := []string{
		"exec",
		"--json",
		"--disable", "apps",
		"--disable", "browser_use",
		"--disable", "computer_use",
		"--disable", "plugins",
		"-c", "shell_environment_policy.inherit=all",
		"--dangerously-bypass-approvals-and-sandbox",
		"-C", req.RepoRoot,
		"resume",
		req.Binding.SessionID,
		prompt,
	}
	cmd := exec.CommandContext(resumeCtx, "codex", args...)
	cmd.Dir = req.RepoRoot
	cmd.Env = append(os.Environ(), "SUBTASK_BRIDGE_RESUME=1", "SUBTASK_BRIDGE_NO_MERGE=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if resumeCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("codex bridge resume timed out after 5m for %s", req.Task)
		}
		return err
	}
	return nil
}

func runCodexBridgeTerminalInjectDelivery(req codexBridgeResumeRequest) error {
	ttyPath, err := resolveCodexLeadTTY(req.Binding)
	if err != nil {
		return err
	}
	sendCodexBridgeDesktopNotification(req, "Waking visible Codex lead")
	prompt := buildCodexBridgeTerminalPrompt(req)
	if err := injectTerminalInput(ttyPath, prompt+"\n"); err != nil {
		if errors.Is(err, os.ErrPermission) {
			fmt.Fprintf(os.Stderr, "subtask: warning: terminal inject denied by macOS, launching visible Warp fallback\n")
			return runCodexBridgeVisibleLaunchDelivery(req, prompt)
		}
		return err
	}
	fmt.Printf("Injected Subtask reply into %s for lead %s: %s.\n", ttyPath, req.Binding.Lead, req.Task)
	return nil
}

func runCodexBridgeNotifyDelivery(req codexBridgeResumeRequest) error {
	sendCodexBridgeDesktopNotification(req, "")
	stage := strings.TrimSpace(req.Stage)
	if stage == "" {
		stage = "(unknown)"
	}
	fmt.Printf("Queued Subtask reply for lead %s: %s (%s). Use `subtask show %s`.\n", req.Binding.Lead, req.Task, stage, req.Task)
	return nil
}

func sendCodexBridgeDesktopNotification(req codexBridgeResumeRequest, titleOverride string) {
	if !desktopNotificationsEnabled() {
		return
	}
	stage := strings.TrimSpace(req.Stage)
	if stage == "" {
		stage = "(unknown)"
	}
	title := "Subtask worker replied"
	if req.Event.Data.Outcome == "error" {
		title = "Subtask worker error"
	}
	if strings.TrimSpace(titleOverride) != "" {
		title = titleOverride
	}
	parts := []string{
		fmt.Sprintf("%s -> %s (%s)", req.Task, req.Binding.Lead, stage),
	}
	if req.Event.Data.DurationMS > 0 {
		parts = append(parts, (time.Duration(req.Event.Data.DurationMS) * time.Millisecond).Round(time.Millisecond).String())
	}
	if req.Event.Data.ToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d tool calls", req.Event.Data.ToolCalls))
	}
	if errText := firstNonEmpty(req.Event.Data.ErrorMessage, req.Event.Data.Error); errText != "" {
		parts = append(parts, errText)
	}
	group := "subtask-codex-bridge-" + safeNotificationGroup(req.Binding.Lead) + "-" + safeNotificationGroup(req.Task) + "-" + safeNotificationGroup(req.Event.Key)
	if err := sendDesktopNotification(title, strings.Join(parts, " - "), group); err != nil {
		fmt.Fprintf(os.Stderr, "subtask: warning: desktop notification failed for %s: %v\n", req.Task, err)
	}
}

func buildCodexBridgePrompt(req codexBridgeResumeRequest) string {
	taskDir := filepath.Join(req.RepoRoot, ".subtask", "tasks", task.EscapeName(req.Task))
	planPath := filepath.Join(taskDir, "PLAN.md")
	stage := req.Stage
	if stage == "" {
		stage = "(unknown)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Subtask worker finished and this task is bound to your lead session.\n\n")
	fmt.Fprintf(&b, "Task: %s\n", req.Task)
	fmt.Fprintf(&b, "Lead: %s\n", req.Binding.Lead)
	fmt.Fprintf(&b, "Stage: %s\n", stage)
	fmt.Fprintf(&b, "Outcome: %s\n", req.Event.Data.Outcome)
	fmt.Fprintf(&b, "Run ID: %s\n", req.Event.Key)
	if req.Event.Data.DurationMS > 0 {
		fmt.Fprintf(&b, "Duration: %s\n", (time.Duration(req.Event.Data.DurationMS) * time.Millisecond).Round(time.Millisecond))
	}
	if req.Event.Data.ToolCalls > 0 {
		fmt.Fprintf(&b, "Tool calls: %d\n", req.Event.Data.ToolCalls)
	}
	if errText := firstNonEmpty(req.Event.Data.ErrorMessage, req.Event.Data.Error); errText != "" {
		fmt.Fprintf(&b, "Error: %s\n", errText)
	}
	fmt.Fprintf(&b, "\nPaths:\n")
	fmt.Fprintf(&b, "- Task folder: %s\n", taskDir)
	fmt.Fprintf(&b, "- PLAN.md: %s\n", planPath)
	fmt.Fprintf(&b, "\nReview commands:\n")
	fmt.Fprintf(&b, "- subtask show %s\n", req.Task)
	fmt.Fprintf(&b, "- subtask log %s\n", req.Task)
	fmt.Fprintf(&b, "- subtask diff --stat %s\n", req.Task)
	fmt.Fprintf(&b, "- subtask workspace %s\n", req.Task)
	fmt.Fprintf(&b, "\nLead instructions:\n")
	fmt.Fprintf(&b, "- Act as the lead for this Subtask task.\n")
	fmt.Fprintf(&b, "- This is an automatic bridge wakeup. Do one focused pass and then stop; do not poll, sleep, watch, or keep the turn open waiting for workers.\n")
	fmt.Fprintf(&b, "- If the stage is plan, read PLAN.md, review risks/conflicts, and either request changes or move the same task to implement when the plan is acceptable.\n")
	fmt.Fprintf(&b, "- If the stage is implement or review, inspect the diff and relevant tests before requesting changes or moving toward ready.\n")
	fmt.Fprintf(&b, "- If you send follow-up work to the worker, use `subtask send --detach ...` so this bridge wakeup can finish immediately.\n")
	fmt.Fprintf(&b, "- Do not merge automatically. Ask the user before running subtask merge.\n")
	fmt.Fprintf(&b, "- Keep work scoped to this task and avoid touching unrelated leaders' tasks.\n")
	return b.String()
}

func buildCodexBridgeTerminalPrompt(req codexBridgeResumeRequest) string {
	stage := strings.TrimSpace(req.Stage)
	if stage == "" {
		stage = "(unknown)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Subtask worker %s: %s\n", firstNonEmpty(req.Event.Data.Outcome, "finished"), req.Task)
	fmt.Fprintf(&b, "Stage: %s. Run ID: %s.\n", stage, req.Event.Key)
	if req.Event.Data.DurationMS > 0 || req.Event.Data.ToolCalls > 0 {
		fmt.Fprintf(&b, "Worker summary: ")
		parts := []string{}
		if req.Event.Data.DurationMS > 0 {
			parts = append(parts, (time.Duration(req.Event.Data.DurationMS) * time.Millisecond).Round(time.Millisecond).String())
		}
		if req.Event.Data.ToolCalls > 0 {
			parts = append(parts, fmt.Sprintf("%d tool calls", req.Event.Data.ToolCalls))
		}
		fmt.Fprintf(&b, "%s.\n", strings.Join(parts, ", "))
	}
	if errText := firstNonEmpty(req.Event.Data.ErrorMessage, req.Event.Data.Error); errText != "" {
		fmt.Fprintf(&b, "Worker error: %s\n", errText)
	}
	fmt.Fprintf(&b, "You are the visible lead for this task. Review it now with `subtask show %s`, `subtask log %s`, and `subtask diff --stat %s`.\n", req.Task, req.Task, req.Task)
	fmt.Fprintf(&b, "If it is a plan-stage reply, read PLAN.md and either request changes or move the same task to implement. If it is implement/review, inspect diff and tests before moving ready.\n")
	fmt.Fprintf(&b, "Do not merge automatically; ask Mahir before `subtask merge`. Do one focused pass and stop.\n")
	return b.String()
}

func resolveCodexLeadTTY(binding codexLeadBinding) (string, error) {
	if ttyPath := normalizeTTYPath(binding.TTY); ttyPath != "" {
		return ttyPath, nil
	}
	ttyPath, ok, err := detectCodexResumeTTY(binding.SessionID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("could not find a visible `codex resume %s` terminal; bind with --tty /dev/ttysXXX or reopen the visible Codex CLI session", binding.SessionID)
	}
	return ttyPath, nil
}

func detectCodexResumeTTY(sessionID string) (string, bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", false, fmt.Errorf("session id is required")
	}
	out, err := exec.Command("ps", "-axo", "pid=,tty=,command=").Output()
	if err != nil {
		return "", false, err
	}
	return parseCodexResumeTTY(string(out), sessionID)
}

func parseCodexResumeTTY(psOutput, sessionID string) (string, bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	for _, line := range strings.Split(psOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		ttyName := fields[1]
		if ttyName == "" || ttyName == "??" || ttyName == "?" {
			continue
		}
		cmdFields := fields[2:]
		cmdText := strings.Join(cmdFields, " ")
		if !strings.Contains(cmdText, "codex") || strings.Contains(cmdText, "codex exec") {
			continue
		}
		if !visibleCodexResumeArgs(cmdFields, sessionID) {
			continue
		}
		return normalizeTTYPath(ttyName), true, nil
	}
	return "", false, nil
}

func visibleCodexResumeArgs(args []string, sessionID string) bool {
	for i, arg := range args {
		base := filepath.Base(arg)
		if base != "codex" && !strings.Contains(base, "codex") {
			continue
		}
		if i+2 >= len(args) {
			continue
		}
		if args[i+1] == "exec" {
			continue
		}
		if args[i+1] == "resume" && args[i+2] == sessionID {
			return true
		}
	}
	for i, arg := range args {
		if arg == "exec" {
			return false
		}
		if arg == "resume" && i+1 < len(args) && args[i+1] == sessionID {
			return strings.Contains(strings.Join(args[:i], " "), "codex")
		}
	}
	return false
}

func normalizeTTYPath(ttyPath string) string {
	ttyPath = strings.TrimSpace(ttyPath)
	if ttyPath == "" || ttyPath == "??" || ttyPath == "?" {
		return ""
	}
	if strings.HasPrefix(ttyPath, "/dev/") {
		return ttyPath
	}
	return filepath.Join("/dev", ttyPath)
}

func writeCodexBridgeWarpLaunchFiles(req codexBridgeResumeRequest, prompt string) (string, string, error) {
	dir := filepath.Join(codexBridgeDir(), "warp-launches")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	name := safeNotificationGroup(req.Binding.Lead) + "-" + safeNotificationGroup(req.Event.Key)
	if name == "-" {
		name = fmt.Sprintf("launch-%d", time.Now().UnixNano())
	}
	promptPath := filepath.Join(dir, name+".txt")
	launchPath := filepath.Join(dir, name+".yaml")
	if err := os.WriteFile(promptPath, []byte(prompt+"\n"), 0o600); err != nil {
		return "", "", err
	}
	command := "codex resume " + shellSingleQuote(req.Binding.SessionID) + " \"$(cat " + shellSingleQuote(promptPath) + ")\""
	var launch strings.Builder
	fmt.Fprintf(&launch, "# Warp Launch Configuration\n---\n")
	fmt.Fprintf(&launch, "name: %s\n", yamlString("Subtask Codex Wakeup - "+req.Binding.Lead))
	fmt.Fprintf(&launch, "windows:\n")
	fmt.Fprintf(&launch, "  - tabs:\n")
	fmt.Fprintf(&launch, "      - title: %s\n", yamlString("Subtask: "+req.Task))
	fmt.Fprintf(&launch, "        layout:\n")
	fmt.Fprintf(&launch, "          cwd: %s\n", yamlString(req.RepoRoot))
	fmt.Fprintf(&launch, "          commands:\n")
	fmt.Fprintf(&launch, "            - exec: %s\n", yamlString(command))
	fmt.Fprintf(&launch, "        color: green\n")
	if err := os.WriteFile(launchPath, []byte(launch.String()), 0o600); err != nil {
		return "", "", err
	}
	return promptPath, launchPath, nil
}

func openWarpLaunchConfig(launchPath string) error {
	launchURL := "warp://launch/" + url.PathEscape(launchPath)
	commands := []string{
		launchURL,
		launchPath,
	}
	var errs []string
	for _, target := range commands {
		cmd := exec.Command("open", target)
		if out, err := cmd.CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("open %s: %v %s", target, err, strings.TrimSpace(string(out))))
			continue
		}
		return nil
	}
	return fmt.Errorf("open visible Warp wakeup failed: %s", strings.Join(errs, "; "))
}

func yamlString(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(data)
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func codexBridgeDir() string {
	return filepath.Join(task.InternalDir(), "codex-bridge")
}

func codexBridgeActiveResumeDir() string {
	return filepath.Join(codexBridgeDir(), "active-resumes")
}

func codexBridgeBindingsPath() string {
	return filepath.Join(codexBridgeDir(), "bindings.json")
}

func codexBridgeDeliveriesPath() string {
	return filepath.Join(codexBridgeDir(), "deliveries.json")
}

func markCodexBridgeActiveResume(req codexBridgeResumeRequest, ttl time.Duration) (func(), error) {
	dir := codexBridgeActiveResumeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return func() {}, err
	}
	name := safeNotificationGroup(req.Binding.Lead) + "-" + safeNotificationGroup(req.Event.Key)
	if name == "-" {
		name = fmt.Sprintf("resume-%d", time.Now().UnixNano())
	}
	path := filepath.Join(dir, name+".json")
	active := codexBridgeActiveResume{
		Lead:      req.Binding.Lead,
		SessionID: req.Binding.SessionID,
		Task:      req.Task,
		RunID:     req.Event.Key,
		PID:       os.Getpid(),
		ExpiresAt: time.Now().UTC().Add(ttl),
	}
	if err := writeCodexBridgeJSON(path, active); err != nil {
		return func() {}, err
	}
	return func() { _ = os.Remove(path) }, nil
}

func codexBridgeActiveResumeBlocksMerge(now time.Time) bool {
	entries, err := os.ReadDir(codexBridgeActiveResumeDir())
	if err != nil {
		return false
	}
	blocked := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(codexBridgeActiveResumeDir(), entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var active codexBridgeActiveResume
		if err := json.Unmarshal(data, &active); err != nil {
			if info, statErr := entry.Info(); statErr == nil && now.Sub(info.ModTime()) < 10*time.Minute {
				blocked = true
				continue
			}
			_ = os.Remove(path)
			continue
		}
		if active.ExpiresAt.IsZero() || now.Before(active.ExpiresAt) {
			blocked = true
			continue
		}
		_ = os.Remove(path)
	}
	return blocked
}

func loadCodexBridgeState() (*codexBridgeState, error) {
	path := codexBridgeBindingsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &codexBridgeState{}, nil
		}
		return nil, err
	}
	var state codexBridgeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func saveCodexBridgeState(state *codexBridgeState) error {
	if state == nil {
		state = &codexBridgeState{}
	}
	sortCodexBindings(state.Bindings)
	return writeCodexBridgeJSON(codexBridgeBindingsPath(), state)
}

func loadCodexBridgeDeliveries() (*codexBridgeDeliveries, error) {
	path := codexBridgeDeliveriesPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &codexBridgeDeliveries{Deliveries: map[string]codexBridgeDelivery{}}, nil
		}
		return nil, err
	}
	var deliveries codexBridgeDeliveries
	if err := json.Unmarshal(data, &deliveries); err != nil {
		return nil, err
	}
	if deliveries.Deliveries == nil {
		deliveries.Deliveries = map[string]codexBridgeDelivery{}
	}
	return &deliveries, nil
}

func saveCodexBridgeDeliveries(deliveries *codexBridgeDeliveries) error {
	if deliveries == nil {
		deliveries = &codexBridgeDeliveries{Deliveries: map[string]codexBridgeDelivery{}}
	}
	if deliveries.Deliveries == nil {
		deliveries.Deliveries = map[string]codexBridgeDelivery{}
	}
	return writeCodexBridgeJSON(codexBridgeDeliveriesPath(), deliveries)
}

func writeCodexBridgeJSON(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func sortCodexBindings(bindings []codexLeadBinding) {
	sort.SliceStable(bindings, func(i, j int) bool {
		a, b := bindings[i], bindings[j]
		if a.Task != b.Task {
			return a.Task < b.Task
		}
		if a.TaskPrefix != b.TaskPrefix {
			return a.TaskPrefix < b.TaskPrefix
		}
		return a.Lead < b.Lead
	})
}

func codexDeliveryID(taskName, runID string) string {
	return taskName + "\x00" + runID
}

func withCodexLeadLock(lead string, fn func() error) (bool, error) {
	safeLead := safeNotificationGroup(lead)
	if safeLead == "" {
		safeLead = "lead"
	}
	path := filepath.Join(codexBridgeDir(), "locks", safeLead+".lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return false, err
	}
	locked, err := filelock.TryLockExclusive(f)
	if err != nil {
		_ = f.Close()
		return false, err
	}
	if !locked {
		_ = f.Close()
		return false, nil
	}
	defer func() {
		_ = filelock.Unlock(f)
		_ = f.Close()
	}()
	if err := fn(); err != nil {
		return false, err
	}
	return true, nil
}

func withCodexBridgeStoreLock(fn func() error) error {
	path := filepath.Join(codexBridgeDir(), "store.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	if err := filelock.LockExclusive(f); err != nil {
		_ = f.Close()
		return err
	}
	defer func() {
		_ = filelock.Unlock(f)
		_ = f.Close()
	}()
	return fn()
}

func printCodexBridgeBindings(state *codexBridgeState) {
	fmt.Println("Bindings:")
	if state == nil || len(state.Bindings) == 0 {
		fmt.Println("  (none)")
		return
	}
	for _, b := range state.Bindings {
		target := b.Task
		if target == "" {
			target = b.TaskPrefix + "*"
		}
		details := []string{b.SessionID, b.deliveryMode()}
		if ttyPath := normalizeTTYPath(b.TTY); ttyPath != "" {
			details = append(details, ttyPath)
		} else if b.deliveryMode() == codexBridgeDeliveryTerminalInject {
			details = append(details, "auto-tty")
		}
		fmt.Printf("  %s -> %s (%s)\n", target, b.Lead, strings.Join(details, ", "))
	}
}
