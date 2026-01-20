package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	petname "github.com/dustinkirkland/golang-petname"
	"github.com/zippoxer/subtask/pkg/harness"
	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/workspace"
)

// AskCmd implements 'subtask ask'.
type AskCmd struct {
	Prompt   string `arg:"" optional:"" help:"Question or prompt (or use stdin)"`
	FollowUp string `name:"follow-up" help:"Continue from task name, session name, or session ID"`
	Model    string `help:"Override model for this prompt (does not persist)"`
	// Reasoning is codex-only (maps to model_reasoning_effort); not persisted.
	Reasoning string `help:"Override reasoning for this prompt (codex-only; does not persist)"`
}

// Run executes the ask command.
func (c *AskCmd) Run() error {
	// Read prompt from stdin if not provided
	prompt := c.Prompt
	if prompt == "" {
		prompt = readStdinIfAvailable()
	}

	if prompt == "" {
		return fmt.Errorf("prompt is required\n\n" +
			"Provide a prompt as argument or via stdin (heredoc/pipe)")
	}

	// Load config for harness
	cfg, err := workspace.LoadConfig()
	if err != nil {
		return err
	}
	if err := workspace.ValidateReasoningFlag(cfg.Harness, c.Reasoning); err != nil {
		return err
	}

	// Get cwd
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	// Resolve --follow-up to session ID and petname
	var continueFrom string
	var sessionName string // petname for this session
	if c.FollowUp != "" {
		// If continuing from a task, ensure harness matches (sessions are not compatible across harnesses).
		if st, err := task.LoadState(c.FollowUp); err == nil && st != nil && st.SessionID != "" {
			if err := enforceTaskHarnessMatch(c.FollowUp, st, cfg.Harness); err != nil {
				return err
			}
		}
		continueFrom, sessionName = resolveAskContext(c.FollowUp)
	}

	// Build prompt with safety prefix
	fullPrompt := "The following is just a question. Do NOT make any modifications or take any actions unless explicitly requested.\n\n" + prompt

	// Create harness and run
	model := workspace.ResolveModel(cfg, nil, c.Model)
	reasoning := workspace.ResolveReasoning(cfg, nil, c.Reasoning)
	h, err := harness.New(workspace.ConfigWithModelReasoning(cfg, model, reasoning))
	if err != nil {
		return err
	}

	printInfo("[Waiting for reply...]")
	fmt.Println()
	fmt.Println()
	printInfo("Tip: Don't check or poll, you'll be notified when done.")

	result, err := h.Run(context.Background(), cwd, fullPrompt, continueFrom, harness.Callbacks{})
	if err != nil {
		return err
	}

	// Generate petname for new sessions
	if sessionName == "" {
		sessionName = generateSessionName()
	}

	// Save conversation and UUID mapping
	convPath, err := saveAskConversation(sessionName, result.SessionID, prompt, result.Reply)
	if err != nil {
		printWarning(fmt.Sprintf("failed to save conversation: %v", err))
	}

	// Print reply
	fmt.Println()
	fmt.Println(result.Reply)
	fmt.Println()

	// Print session info for resuming
	fmt.Printf("Session: %s\n", sessionName)
	fmt.Printf("Resume:  subtask ask --follow-up %s \"...\"\n", sessionName)
	if convPath != "" {
		fmt.Printf("Log:     %s\n", convPath)
	}

	return nil
}

// ConversationsDir returns ~/.subtask/conversations.
func ConversationsDir() string {
	return filepath.Join(task.GlobalDir(), "conversations")
}

// resolveAskContext resolves a context string to (sessionID, sessionName).
// Checks: task name → petname → raw session ID
func resolveAskContext(ctx string) (sessionID, sessionName string) {
	// Try as task name first
	if state, err := task.LoadState(ctx); err == nil && state != nil && state.SessionID != "" {
		return state.SessionID, ""
	}

	// Try as petname (check for .uuid file)
	uuidPath := filepath.Join(ConversationsDir(), ctx+".uuid")
	if data, err := os.ReadFile(uuidPath); err == nil {
		return strings.TrimSpace(string(data)), ctx
	}

	// Assume it's a raw session ID
	return ctx, ""
}

// generateSessionName generates a unique petname for a session.
func generateSessionName() string {
	dir := ConversationsDir()
	os.MkdirAll(dir, 0755)

	for i := 0; i < 100; i++ { // Max retries
		name := petname.Generate(3, "-")
		convPath := filepath.Join(dir, name+".txt")
		if _, err := os.Stat(convPath); os.IsNotExist(err) {
			return name
		}
	}

	// Fallback: add random suffix (shouldn't happen in practice)
	return petname.Generate(4, "-")
}

// saveAskConversation saves the conversation and UUID mapping.
// Returns the conversation file path.
func saveAskConversation(sessionName, sessionID, prompt, reply string) (string, error) {
	dir := ConversationsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	convPath := filepath.Join(dir, sessionName+".txt")
	uuidPath := filepath.Join(dir, sessionName+".uuid")

	// Save UUID mapping (only if new)
	if _, err := os.Stat(uuidPath); os.IsNotExist(err) {
		if err := os.WriteFile(uuidPath, []byte(sessionID+"\n"), 0644); err != nil {
			return "", err
		}
	}

	// Append to conversation
	f, err := os.OpenFile(convPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}
	defer f.Close()

	fmt.Fprintf(f, "<user>\n%s\n</user>\n\n<assistant>\n%s\n</assistant>\n\n", prompt, reply)
	return convPath, nil
}
