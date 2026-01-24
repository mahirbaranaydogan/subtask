package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zippoxer/subtask/internal/homedir"
)

// CodexHarness implements Harness for the Codex CLI.
type CodexHarness struct {
	cli       cliSpec
	Model     string
	Reasoning string
}

// CodexEvent represents a JSONL event from codex exec --json.
type CodexEvent struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id,omitempty"` // in thread.started
	Message  string `json:"message,omitempty"`   // in error event
	Item     *struct {
		ID      string `json:"id,omitempty"`
		Type    string `json:"type,omitempty"` // command_execution, agent_message, reasoning
		Text    string `json:"text,omitempty"`
		Command string `json:"command,omitempty"`
	} `json:"item,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"` // in turn.failed
}

const codexMaxJSONLLineBytes = 32 * 1024 * 1024 // 32MB

func processCodexJSONLLine(line []byte, result *Result, cb Callbacks) {
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}

	var event CodexEvent
	if err := json.Unmarshal(line, &event); err != nil {
		// Not JSON, skip.
		return
	}

	switch event.Type {
	case "thread.started":
		result.SessionID = event.ThreadID
		result.PromptDelivered = true
		if cb.OnSessionStart != nil {
			cb.OnSessionStart(event.ThreadID)
		}

	case "item.started":
		if event.Item != nil && event.Item.Type == "command_execution" {
			if cb.OnToolCall != nil {
				cb.OnToolCall(time.Now())
			}
		}

		case "item.completed":
		if event.Item != nil && event.Item.Type == "agent_message" {
			result.AgentReplied = true
			// Note: We also read from -o file, but capture here too.
			if event.Item.Text != "" {
				result.Reply = event.Item.Text
			}
		}

	case "error":
		result.Error = event.Message

	case "turn.completed":
		// Codex may emit transient "error" events (e.g. brief network failures)
		// even when the overall turn succeeds. If the turn completed, treat any
		// prior stream error as recovered.
		result.Error = ""
		result.TurnFailed = false

	case "turn.failed":
		if event.Error != nil {
			result.Error = event.Error.Message
		}
		result.TurnFailed = true
	}
}

func parseCodexExecJSONL(r io.Reader, result *Result, cb Callbacks, maxLineBytes int) error {
	// Codex can emit large JSONL lines (reasoning and/or aggregated tool output).
	//
	// bufio.Scanner has a hard token limit; exceeding it can silently stop parsing and can
	// deadlock the process (stdout pipe fills, codex blocks on write, subtask blocks on Wait()).
	//
	// Use bufio.Reader + ReadSlice to keep draining stdout even if a line is unexpectedly huge.
	if maxLineBytes <= 0 {
		maxLineBytes = codexMaxJSONLLineBytes
	}

	br := bufio.NewReaderSize(r, 256*1024)

	var (
		firstErr error

		accum   []byte
		tooLong bool
	)

	for {
		frag, err := br.ReadSlice('\n')
		switch {
		case err == nil:
			if tooLong {
				// Discard the remainder of an overlong line; reset at newline boundary.
				tooLong = false
				accum = accum[:0]
				continue
			}
			if len(accum)+len(frag) > maxLineBytes {
				if firstErr == nil {
					firstErr = fmt.Errorf("codex json stream line exceeded %d bytes", maxLineBytes)
				}
				accum = accum[:0]
				continue
			}
			accum = append(accum, frag...)
			processCodexJSONLLine(bytes.TrimSpace(accum), result, cb)
			accum = accum[:0]
			continue

		case errors.Is(err, bufio.ErrBufferFull):
			if tooLong {
				// Keep draining until newline.
				continue
			}
			if len(accum)+len(frag) > maxLineBytes {
				if firstErr == nil {
					firstErr = fmt.Errorf("codex json stream line exceeded %d bytes", maxLineBytes)
				}
				tooLong = true
				accum = accum[:0]
				continue
			}
			accum = append(accum, frag...)
			continue

		case errors.Is(err, io.EOF):
			// Process any trailing data (may be a partial last line without newline).
			if len(frag) > 0 && !tooLong {
				if len(accum)+len(frag) > maxLineBytes {
					if firstErr == nil {
						firstErr = fmt.Errorf("codex json stream line exceeded %d bytes", maxLineBytes)
					}
				} else {
					accum = append(accum, frag...)
					processCodexJSONLLine(bytes.TrimSpace(accum), result, cb)
				}
			}
			return firstErr

		default:
			// A real read error.
			if firstErr == nil {
				firstErr = err
			}
			return firstErr
		}
	}
}

// Run executes Codex with the given prompt. Blocks until completion.
func (c *CodexHarness) Run(ctx context.Context, cwd, prompt, continueFrom string, cb Callbacks) (*Result, error) {
	// Flags must come before positionals
	args := []string{"exec", "--json", "--dangerously-bypass-approvals-and-sandbox", "--enable", "web_search_request"}

	if c.Model != "" {
		args = append(args, "-m", c.Model)
	}
	if c.Reasoning != "" {
		args = append(args, "-c", fmt.Sprintf("model_reasoning_effort=%s", c.Reasoning))
	}

	// Positionals come last (after -o is added by runCodexCommand)
	var positionals []string
	if continueFrom != "" {
		positionals = []string{"resume", continueFrom, prompt}
	} else {
		positionals = []string{prompt}
	}

	return c.runCodexCommand(ctx, cwd, args, positionals, cb, true)
}

// runCodexCommand is the shared infrastructure for running codex commands with JSONL output.
// flags are CLI flags, positionals are appended after -o flag.
// useOutputFile controls whether to use -o flag (not supported by all subcommands like review).
func (c *CodexHarness) runCodexCommand(ctx context.Context, cwd string, flags, positionals []string, cb Callbacks, useOutputFile bool) (*Result, error) {
	args := flags
	var tmpPath string
	if useOutputFile {
		// Create temp file for output (agent's final message)
		tmpFile, err := os.CreateTemp("", "codex-reply-*.txt")
		if err != nil {
			return nil, fmt.Errorf("failed to create temp file: %w", err)
		}
		tmpPath = tmpFile.Name()
		tmpFile.Close()
		defer os.Remove(tmpPath)
		args = append(args, "-o", tmpPath)
	}
	// Positionals come after all flags
	args = append(args, positionals...)

	cmd, err := commandForCLI(ctx, c.effectiveCLI(), args)
	if err != nil {
		return nil, err
	}
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), fmt.Sprintf("WORKSPACE_ID=%d", extractWorkspaceID(cwd)))

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start codex: %w", err)
	}

	// Parse JSONL events
	result := &Result{}

	// Collect stderr concurrently for better error messages.
	var stderrBuf strings.Builder
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(io.MultiWriter(&stderrBuf, os.Stderr), stderr)
	}()

	streamErr := parseCodexExecJSONL(stdout, result, cb, codexMaxJSONLLineBytes)

	// Wait for command to finish
	cmdErr := cmd.Wait()
	<-stderrDone

	// Read reply from output file (more reliable than parsing events)
	var replyFileErr error
	if tmpPath != "" {
		replyFromFile, err := os.ReadFile(tmpPath)
		replyFileErr = err
		if err == nil && len(replyFromFile) > 0 {
			result.Reply = string(replyFromFile)
			result.AgentReplied = true
		}
	}

	successReply := strings.TrimSpace(result.Reply) != ""

	// Codex can emit transient "error" events during a successful run (e.g. it retries
	// internally). If we have a successful exit code and a valid final reply, treat any
	// remaining stream error as recovered.
	if result.Error != "" && !result.TurnFailed && cmdErr == nil && successReply {
		result.Error = ""
	}

	// If command failed and we don't have a specific error, use generic message
	if cmdErr != nil && result.Error == "" {
		result.Error = strings.TrimSpace(stderrBuf.String())
		if result.Error == "" {
			result.Error = cmdErr.Error()
		}
		return result, fmt.Errorf("codex failed: %w", cmdErr)
	}

	// If we got an error event and we don't have a success signal, return it.
	if result.Error != "" {
		return result, fmt.Errorf("codex error: %s", result.Error)
	}

	// Defensive: avoid treating "success with empty reply" as a successful run.
	// When this happens, it usually indicates a CLI/harness mismatch (e.g., output file not
	// written, JSON stream parsing interrupted, etc.).
	if !successReply {
		var parts []string
		parts = append(parts, "codex produced empty reply")
		if tmpPath != "" {
			if replyFileErr != nil {
				parts = append(parts, fmt.Sprintf("last-message file read failed: %v", replyFileErr))
			} else {
				parts = append(parts, "last-message file empty")
			}
		}
		if streamErr != nil {
			parts = append(parts, fmt.Sprintf("json stream error: %v", streamErr))
		}
		if s := strings.TrimSpace(stderrBuf.String()); s != "" {
			parts = append(parts, fmt.Sprintf("stderr: %s", s))
		}
		result.Error = strings.Join(parts, "; ")
		return result, fmt.Errorf("codex failed: %s", result.Error)
	}

	return result, nil
}

// Review runs codex exec review using the shared command infrastructure.
func (c *CodexHarness) Review(cwd string, target ReviewTarget, instructions string) (string, error) {
	// exec-level flags come before the "review" subcommand
	flags := []string{"exec", "--json", "--dangerously-bypass-approvals-and-sandbox"}

	if c.Model != "" {
		flags = append(flags, "-m", c.Model)
	}
	if c.Reasoning != "" {
		flags = append(flags, "-c", "model_reasoning_effort="+c.Reasoning)
	}

	// "review" subcommand and its flags/positionals
	flags = append(flags, "review")

	switch {
	case target.Uncommitted:
		flags = append(flags, "--uncommitted")
	case target.BaseBranch != "":
		flags = append(flags, "--base", target.BaseBranch)
	case target.Commit != "":
		flags = append(flags, "--commit", target.Commit)
	default:
		flags = append(flags, "--uncommitted")
	}

	// Instructions are the positional prompt for review
	var positionals []string
	if instructions != "" {
		positionals = []string{instructions}
	}

	result, err := c.runCodexCommand(context.Background(), cwd, flags, positionals, Callbacks{}, false)
	if err != nil {
		return "", err
	}
	return result.Reply, nil
}

func (c *CodexHarness) effectiveCLI() cliSpec {
	if strings.TrimSpace(c.cli.Exec) == "" {
		return cliSpec{Exec: "codex"}
	}
	return c.cli
}

func (c *CodexHarness) MigrateSession(sessionID, oldCwd, newCwd string) error {
	// Codex session IDs are global, so no migration is needed.
	return nil
}

func (c *CodexHarness) DuplicateSession(sessionID, oldCwd, newCwd string) (string, error) {
	if sessionID == "" {
		return "", nil
	}

	src, err := findCodexSessionFile(sessionID)
	if err != nil {
		return "", err
	}

	newSessionID, err := newUUIDv4()
	if err != nil {
		return "", err
	}

	dir := filepath.Dir(src)
	base := filepath.Base(src)
	newBase := strings.Replace(base, sessionID, newSessionID, 1)
	if newBase == base {
		// Unexpected (filename should contain session ID), but fall back to suffixing.
		newBase = strings.TrimSuffix(base, ".jsonl") + "-" + newSessionID + ".jsonl"
	}
	dst := filepath.Join(dir, newBase)

	// Extremely unlikely, but avoid clobbering an existing file.
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("duplicate session destination already exists: %s", dst)
	}

	if err := copyCodexSessionWithNewID(src, dst, sessionID, newSessionID); err != nil {
		return "", err
	}
	return newSessionID, nil
}

func findCodexSessionFile(sessionID string) (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", err
	}

	sessionsDir := filepath.Join(home, ".codex", "sessions")

	var found string
	err = filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if strings.Contains(filepath.Base(path), sessionID) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return "", err
	}
	if found == "" {
		return "", os.ErrNotExist
	}
	return found, nil
}

func copyCodexSessionWithNewID(src, dst, oldSessionID, newSessionID string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
	if err != nil {
		return err
	}

	reader := bufio.NewReader(in)
	updated := false

	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			if !updated && bytes.Contains(line, []byte(`"type":"session_meta"`)) {
				trimmed := bytes.TrimSpace(line)
				var ev struct {
					Timestamp string         `json:"timestamp"`
					Type      string         `json:"type"`
					Payload   map[string]any `json:"payload"`
				}
				if err := json.Unmarshal(trimmed, &ev); err == nil && ev.Type == "session_meta" && ev.Payload != nil {
					if id, ok := ev.Payload["id"].(string); ok && id == oldSessionID {
						ev.Payload["id"] = newSessionID
						b, err := json.Marshal(ev)
						if err == nil {
							if _, err := out.Write(b); err != nil {
								out.Close()
								_ = os.Remove(tmp)
								return err
							}
							if len(line) > 0 && line[len(line)-1] == '\n' {
								if _, err := out.Write([]byte("\n")); err != nil {
									out.Close()
									_ = os.Remove(tmp)
									return err
								}
							}
							updated = true
							if readErr == io.EOF {
								break
							}
							if readErr != nil {
								out.Close()
								_ = os.Remove(tmp)
								return readErr
							}
							continue
						}
					}
				}
			}

			if _, err := out.Write(line); err != nil {
				out.Close()
				_ = os.Remove(tmp)
				return err
			}
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			out.Close()
			_ = os.Remove(tmp)
			return readErr
		}
	}

	if !updated {
		out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to update codex session_meta id in duplicated session")
	}

	if err := out.Sync(); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	return os.Rename(tmp, dst)
}

// extractWorkspaceID extracts the workspace number from a workspace path.
// e.g., "/Users/foo/.subtask/workspaces/-Users-foo-code-project--2" → 2
func extractWorkspaceID(workspacePath string) int {
	base := filepath.Base(workspacePath)
	if idx := strings.LastIndex(base, "--"); idx != -1 {
		if id, err := strconv.Atoi(base[idx+2:]); err == nil {
			return id
		}
	}
	return 0
}
