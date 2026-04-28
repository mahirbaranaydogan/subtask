package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	Status CodexBridgeStatusCmd `cmd:"" help:"Show Codex lead bridge status"`
	Watch  CodexBridgeWatchCmd  `cmd:"" help:"Watch worker replies and notify or resume bound Codex leads"`
}

type CodexBridgeBindCmd struct {
	Lead       string `help:"Lead name" required:""`
	Session    string `help:"Codex session/thread id to resume" required:""`
	Task       string `help:"Exact task name to bind"`
	TaskPrefix string `name:"task-prefix" help:"Task name prefix to bind"`
	Delivery   string `help:"Delivery mode: notify or exec-resume" default:"notify"`
	FromNow    bool   `name:"from-now" help:"Do not deliver worker replies that already existed before this binding"`
}

type CodexBridgeUnbindCmd struct {
	Lead       string `help:"Remove all bindings for this lead"`
	Task       string `help:"Exact task binding to remove"`
	TaskPrefix string `name:"task-prefix" help:"Task prefix binding to remove"`
}

type CodexBridgeListCmd struct{}

type CodexBridgeStatusCmd struct{}

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

var runCodexBridgeResume = runCodexBridgeResumeCommand

const (
	codexBridgeDeliveryNotify     = "notify"
	codexBridgeDeliveryExecResume = "exec-resume"
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
	if delivery != codexBridgeDeliveryNotify && delivery != codexBridgeDeliveryExecResume {
		return codexLeadBinding{}, fmt.Errorf("--delivery must be %q or %q", codexBridgeDeliveryNotify, codexBridgeDeliveryExecResume)
	}
	now := time.Now().UTC()
	return codexLeadBinding{
		Lead:       lead,
		SessionID:  session,
		Task:       taskName,
		TaskPrefix: prefix,
		Delivery:   delivery,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
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
	var delivered int
	err := withCodexBridgeStoreLock(func() error {
		var err error
		delivered, err = codexBridgeDeliverOnceLocked(ctx, repoRoot)
		return err
	})
	return delivered, err
}

func codexBridgeDeliverOnceLocked(ctx context.Context, repoRoot string) (int, error) {
	state, err := loadCodexBridgeState()
	if err != nil {
		return 0, err
	}
	if len(state.Bindings) == 0 {
		return 0, nil
	}
	deliveries, err := loadCodexBridgeDeliveries()
	if err != nil {
		return 0, err
	}
	events, err := latestFinishedEvents()
	if err != nil {
		return 0, err
	}
	delivered := 0
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
			return delivered, err
		}
		req := codexBridgeResumeRequest{
			RepoRoot: repoRoot,
			Task:     ev.Task,
			Stage:    strings.TrimSpace(tail.Stage),
			Event:    ev,
			Binding:  match.Binding,
		}
		ok, err := withCodexLeadLock(match.Binding.Lead, func() error {
			return runCodexBridgeResume(ctx, req)
		})
		if err != nil {
			return delivered, err
		}
		if !ok {
			continue
		}
		deliveries.Deliveries[deliveryID] = codexBridgeDelivery{
			Task:      ev.Task,
			RunID:     ev.Key,
			Lead:      match.Binding.Lead,
			SessionID: match.Binding.SessionID,
			Outcome:   ev.Data.Outcome,
			Mode:      match.Binding.deliveryMode(),
			Delivered: time.Now().UTC(),
		}
		if err := saveCodexBridgeDeliveries(deliveries); err != nil {
			return delivered, err
		}
		delivered++
	}
	return delivered, nil
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
	if req.Binding.deliveryMode() == codexBridgeDeliveryNotify {
		return runCodexBridgeNotifyDelivery(req)
	}
	sendCodexBridgeDesktopNotification(req, "Waking Codex lead")
	prompt := buildCodexBridgePrompt(req)
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
	cmd := exec.CommandContext(ctx, "codex", args...)
	cmd.Dir = req.RepoRoot
	cmd.Env = append(os.Environ(), "SUBTASK_BRIDGE_RESUME=1", "SUBTASK_BRIDGE_NO_MERGE=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
	fmt.Fprintf(&b, "- If the stage is plan, read PLAN.md, review risks/conflicts, and either request changes or move the same task to implement when the plan is acceptable.\n")
	fmt.Fprintf(&b, "- If the stage is implement or review, inspect the diff and relevant tests before requesting changes or moving toward ready.\n")
	fmt.Fprintf(&b, "- Do not merge automatically. Ask the user before running subtask merge.\n")
	fmt.Fprintf(&b, "- Keep work scoped to this task and avoid touching unrelated leaders' tasks.\n")
	return b.String()
}

func codexBridgeDir() string {
	return filepath.Join(task.InternalDir(), "codex-bridge")
}

func codexBridgeBindingsPath() string {
	return filepath.Join(codexBridgeDir(), "bindings.json")
}

func codexBridgeDeliveriesPath() string {
	return filepath.Join(codexBridgeDir(), "deliveries.json")
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
		fmt.Printf("  %s -> %s (%s, %s)\n", target, b.Lead, b.SessionID, b.deliveryMode())
	}
}
