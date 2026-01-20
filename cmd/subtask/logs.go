package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/zippoxer/subtask/pkg/logs"
	"github.com/zippoxer/subtask/pkg/render"
	"github.com/zippoxer/subtask/pkg/task"
)

// LogsCmd implements 'subtask logs'.
type LogsCmd struct {
	TaskOrSession string `arg:"" help:"Task name or session ID"`
	Limit         int    `short:"n" help:"Show only the last N entries" default:"0"`
	Since         string `help:"Show entries since timestamp (e.g., '5m', '1h', '2024-01-01T10:00:00Z')"`
	Follow        bool   `short:"f" help:"Follow log output (stream new entries)"`
	Timestamps    bool   `short:"t" help:"Show timestamps"`
	NoTrunc       bool   `help:"Don't truncate output"`
}

type harnessLogBackend struct {
	name      string
	parser    logs.Parser
	locator   logs.Locator
	parseLine func([]byte) *logs.LogEntry
}

// Run executes the logs command.
func (c *LogsCmd) Run() error {
	if _, err := preflightProject(); err != nil {
		return err
	}

	backends := []harnessLogBackend{
		{name: "codex", parser: &logs.CodexParser{}, locator: &logs.CodexParser{}, parseLine: parseSingleLineCodex},
		{name: "claude", parser: &logs.ClaudeParser{}, locator: &logs.ClaudeParser{}, parseLine: parseSingleLineClaude},
	}

	// Try to find session file - could be task name or session ID
	sessionFile, backend, sessionID, err := c.resolveSession(backends)
	if err != nil {
		return err
	}
	_ = sessionID // Available for future use (e.g., header display)

	// Parse since filter
	var sinceTime time.Time
	if c.Since != "" {
		sinceTime, err = parseSince(c.Since)
		if err != nil {
			return fmt.Errorf("invalid --since value: %w", err)
		}
	}

	// Set up formatter
	formatter := &logs.Formatter{
		Pretty:     render.Pretty,
		Timestamps: c.Timestamps,
		NoTrunc:    c.NoTrunc,
	}

	if c.Follow {
		return c.streamLogs(sessionFile, backend, formatter, sinceTime)
	}

	return c.showLogs(sessionFile, backend, formatter, sinceTime)
}

// showLogs displays logs from the session file.
func (c *LogsCmd) showLogs(path string, backend harnessLogBackend, formatter *logs.Formatter, since time.Time) error {
	// Collect entries (for limit support we need to buffer)
	var entries []logs.LogEntry
	var sessionInfo *logs.SessionInfo

	info, err := backend.parser.ParseFile(path, func(e logs.LogEntry) {
		// Apply since filter
		if !since.IsZero() && e.Time.Before(since) {
			return
		}
		entries = append(entries, e)
	})
	if err != nil {
		return err
	}
	sessionInfo = info

	// Apply limit (take last N)
	if c.Limit > 0 && len(entries) > c.Limit {
		entries = entries[len(entries)-c.Limit:]
	}

	// Set start time for relative timestamps
	if sessionInfo != nil {
		formatter.StartTime = sessionInfo.StartTime
	} else if len(entries) > 0 {
		formatter.StartTime = entries[0].Time
	}

	// Print entries
	for _, e := range entries {
		line := formatter.Format(e)
		if line == "" {
			continue // Skip empty formatted lines
		}
		fmt.Println(line)
	}

	return nil
}

// streamLogs follows the log file for new entries.
func (c *LogsCmd) streamLogs(path string, backend harnessLogBackend, formatter *logs.Formatter, since time.Time) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// First, show existing entries (respecting limit and since)
	var entries []logs.LogEntry
	var sessionInfo *logs.SessionInfo

	info, err := backend.parser.ParseFile(path, func(e logs.LogEntry) {
		if !since.IsZero() && e.Time.Before(since) {
			return
		}
		entries = append(entries, e)
	})
	if err != nil {
		return err
	}
	sessionInfo = info

	// Apply limit to initial display
	if c.Limit > 0 && len(entries) > c.Limit {
		entries = entries[len(entries)-c.Limit:]
	}

	// Set start time
	if sessionInfo != nil {
		formatter.StartTime = sessionInfo.StartTime
	} else if len(entries) > 0 {
		formatter.StartTime = entries[0].Time
	}

	// Print initial entries
	for _, e := range entries {
		line := formatter.Format(e)
		if line == "" {
			continue
		}
		fmt.Println(line)
	}

	// Now tail the file for new entries
	// Seek to end
	f.Seek(0, io.SeekEnd)

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for {
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			// Parse single line and emit
			entry := backend.parseLine(line)
			if entry != nil {
				if !since.IsZero() && entry.Time.Before(since) {
					continue
				}
				formatted := formatter.Format(*entry)
				if formatted != "" {
					fmt.Println(formatted)
				}
			}
		}

		if err := scanner.Err(); err != nil {
			return err
		}

		// Wait a bit before checking for more
		time.Sleep(100 * time.Millisecond)

		// Re-check file for new content
		// Reset scanner to continue from current position
		scanner = bufio.NewScanner(f)
		scanner.Buffer(buf, 1024*1024)
	}
}

// parseSince parses a duration or timestamp string.
func parseSince(s string) (time.Time, error) {
	// Try relative duration first (e.g., "5m", "1h", "30s")
	if matched, _ := regexp.MatchString(`^\d+[smhd]$`, s); matched {
		unit := s[len(s)-1]
		val, _ := strconv.Atoi(s[:len(s)-1])
		var dur time.Duration
		switch unit {
		case 's':
			dur = time.Duration(val) * time.Second
		case 'm':
			dur = time.Duration(val) * time.Minute
		case 'h':
			dur = time.Duration(val) * time.Hour
		case 'd':
			dur = time.Duration(val) * 24 * time.Hour
		}
		return time.Now().Add(-dur), nil
	}

	// Try ISO timestamp
	for _, layout := range []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("cannot parse %q as duration or timestamp", s)
}

// parseSingleLineCodex is a simplified parser for follow mode.
// It duplicates some logic from CodexParser but for single-line streaming.
func parseSingleLineCodex(line []byte) *logs.LogEntry {
	// Quick JSON parse
	type quickEvent struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
	}
	var event quickEvent
	// Find timestamp and type without full parse
	tsIdx := strings.Index(string(line), `"timestamp":"`)
	typeIdx := strings.Index(string(line), `"type":"`)

	if tsIdx == -1 || typeIdx == -1 {
		return nil
	}

	// Extract timestamp
	tsStart := tsIdx + 13
	tsEnd := strings.Index(string(line[tsStart:]), `"`)
	if tsEnd == -1 {
		return nil
	}
	event.Timestamp = string(line[tsStart : tsStart+tsEnd])

	// Extract type
	typeStart := typeIdx + 8
	typeEnd := strings.Index(string(line[typeStart:]), `"`)
	if typeEnd == -1 {
		return nil
	}
	event.Type = string(line[typeStart : typeStart+typeEnd])

	ts, _ := time.Parse(time.RFC3339Nano, event.Timestamp)
	if ts.IsZero() {
		ts, _ = time.Parse(time.RFC3339, event.Timestamp)
	}

	// Handle based on type
	switch event.Type {
	case "event_msg":
		// Look for agent_message or user_message
		if strings.Contains(string(line), `"type":"agent_message"`) {
			msgIdx := strings.Index(string(line), `"message":"`)
			if msgIdx != -1 {
				start := msgIdx + 11
				end := findStringEnd(line[start:])
				if end > 0 {
					msg := string(line[start : start+end])
					msg = unescapeJSON(msg)
					return &logs.LogEntry{
						Time:    ts,
						Kind:    logs.KindAgentMessage,
						Summary: truncate(msg, 200),
					}
				}
			}
		}
		if strings.Contains(string(line), `"type":"user_message"`) {
			msgIdx := strings.Index(string(line), `"message":"`)
			if msgIdx != -1 {
				start := msgIdx + 11
				end := findStringEnd(line[start:])
				if end > 0 {
					msg := string(line[start : start+end])
					msg = unescapeJSON(msg)
					return &logs.LogEntry{
						Time:    ts,
						Kind:    logs.KindUserMessage,
						Summary: truncate(msg, 200),
					}
				}
			}
		}
		if strings.Contains(string(line), `"type":"agent_reasoning"`) {
			textIdx := strings.Index(string(line), `"text":"`)
			if textIdx != -1 {
				start := textIdx + 8
				end := findStringEnd(line[start:])
				if end > 0 {
					text := string(line[start : start+end])
					text = unescapeJSON(text)
					return &logs.LogEntry{
						Time:    ts,
						Kind:    logs.KindReasoning,
						Summary: truncate(text, 150),
					}
				}
			}
		}

	case "response_item":
		if strings.Contains(string(line), `"type":"function_call"`) &&
			!strings.Contains(string(line), `"type":"function_call_output"`) {
			// Tool call
			nameIdx := strings.Index(string(line), `"name":"`)
			if nameIdx != -1 {
				start := nameIdx + 8
				end := findStringEnd(line[start:])
				if end > 0 {
					name := string(line[start : start+end])
					return &logs.LogEntry{
						Time:     ts,
						Kind:     logs.KindToolCall,
						Summary:  "→ " + name,
						ToolName: name,
					}
				}
			}
		}
	}

	return nil
}

// parseSingleLineClaude parses a single Claude session JSONL line (best-effort).
func parseSingleLineClaude(line []byte) *logs.LogEntry {
	type ev struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp,omitempty"`
		Message   struct {
			Role    string          `json:"role,omitempty"`
			Content json.RawMessage `json:"content,omitempty"`
		} `json:"message,omitempty"`
	}
	var e ev
	if err := json.Unmarshal(line, &e); err != nil {
		return nil
	}
	ts, _ := time.Parse(time.RFC3339Nano, e.Timestamp)
	if ts.IsZero() {
		ts, _ = time.Parse(time.RFC3339, e.Timestamp)
	}

	switch e.Type {
	case "user":
		var s string
		if err := json.Unmarshal(e.Message.Content, &s); err == nil && s != "" {
			return &logs.LogEntry{Time: ts, Kind: logs.KindUserMessage, Summary: truncate(s, 200)}
		}
	case "assistant":
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
			Name string `json:"name,omitempty"`
		}
		if err := json.Unmarshal(e.Message.Content, &parts); err == nil {
			for _, p := range parts {
				if p.Type == "text" && p.Text != "" {
					return &logs.LogEntry{Time: ts, Kind: logs.KindAgentMessage, Summary: truncate(p.Text, 200)}
				}
				if p.Type == "tool_use" && p.Name != "" {
					return &logs.LogEntry{Time: ts, Kind: logs.KindToolCall, Summary: "→ " + p.Name, ToolName: p.Name}
				}
			}
		}
	}
	return nil
}

// findStringEnd finds the end of a JSON string (handling escapes).
func findStringEnd(b []byte) int {
	escaped := false
	for i, c := range b {
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			return i
		}
	}
	return -1
}

// unescapeJSON handles basic JSON string unescaping.
func unescapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `\\`, `\`)
	s = strings.ReplaceAll(s, `\n`, " ")
	s = strings.ReplaceAll(s, `\t`, " ")
	return s
}

// truncate truncates a string to max length.
func truncate(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// resolveSession resolves a task name or session ID to a session file path.
// Returns (sessionFile, backend, sessionID, error).
func (c *LogsCmd) resolveSession(backends []harnessLogBackend) (string, harnessLogBackend, string, error) {
	arg := c.TaskOrSession

	// First, try as a task name
	state, err := task.LoadState(arg)
	if err == nil && state != nil && state.SessionID != "" {
		preferred := sessionHarnessForTask(arg, state)
		candidates := orderBackends(backends, preferred)
		for _, b := range candidates {
			sessionFile, err := b.locator.FindSessionFile(state.SessionID)
			if err == nil {
				return sessionFile, b, state.SessionID, nil
			}
			if !os.IsNotExist(err) {
				return "", harnessLogBackend{}, "", err
			}
		}
		// Session file not found - fall through to try as session ID
	}

	// Try as a session ID directly
	// Session IDs look like UUIDs: 019a48ac-f230-7f23-b587-d7e38f2669cd
	for _, b := range backends {
		sessionFile, err := b.locator.FindSessionFile(arg)
		if err == nil {
			return sessionFile, b, arg, nil
		}
		if !os.IsNotExist(err) {
			return "", harnessLogBackend{}, "", err
		}
	}

	// Neither worked - give helpful error
	if state != nil && state.SessionID != "" {
		return "", harnessLogBackend{}, "", fmt.Errorf("session file not found for task %q\n\nSession ID: %s\nThe session file may have been deleted or moved.", arg, state.SessionID)
	}

	// Check if task exists at all
	if _, taskErr := task.Load(arg); taskErr == nil {
		return "", harnessLogBackend{}, "", fmt.Errorf("task %q has no session (never run?)", arg)
	}

	return "", harnessLogBackend{}, "", fmt.Errorf("no task or session found for %q", arg)
}

func orderBackends(backends []harnessLogBackend, preferred string) []harnessLogBackend {
	if preferred == "" {
		return backends
	}
	var out []harnessLogBackend
	for _, b := range backends {
		if b.name == preferred {
			out = append(out, b)
		}
	}
	for _, b := range backends {
		if b.name != preferred {
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		return backends
	}
	return out
}
