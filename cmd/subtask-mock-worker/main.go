package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

type codexItem struct {
	Type    string `json:"type,omitempty"`
	Text    string `json:"text,omitempty"`
	Command string `json:"command,omitempty"`
}

type codexEvent struct {
	Type     string     `json:"type"`
	ThreadID string     `json:"thread_id,omitempty"`
	Message  string     `json:"message,omitempty"`
	Item     *codexItem `json:"item,omitempty"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type limitedBuffer struct {
	limit     int
	n         int
	truncated bool
	buf       bytes.Buffer
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return b.buf.Write(p)
	}
	remain := b.limit - b.n
	if remain <= 0 {
		b.truncated = true
		b.n += len(p)
		return len(p), nil
	}
	if len(p) > remain {
		_, _ = b.buf.Write(p[:remain])
		b.truncated = true
		b.n += len(p)
		return len(p), nil
	}
	n, err := b.buf.Write(p)
	b.n += len(p)
	return n, err
}

func (b *limitedBuffer) String() string {
	s := b.buf.String()
	if b.truncated {
		s += "\n[output truncated]\n"
	}
	return s
}

func emitJSONL(w io.Writer, ev codexEvent) {
	b, _ := json.Marshal(ev)
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n"))
}

func usage() {
	_, _ = fmt.Fprintln(os.Stderr, "Usage:")
	_, _ = fmt.Fprintln(os.Stderr, "  subtask-mock-worker exec --json [--cwd <path>] [--resume <sessionID>] <prompt>")
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "exec":
		runExec(os.Args[2:])
	default:
		usage()
	}
}

func runExec(args []string) {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		jsonOut bool
		cwd     string
		resume  string
	)
	fs.BoolVar(&jsonOut, "json", false, "emit JSONL events to stdout")
	fs.StringVar(&cwd, "cwd", "", "working directory for commands (default: current)")
	fs.StringVar(&resume, "resume", "", "resume existing session id")

	if err := fs.Parse(args); err != nil {
		usage()
	}
	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		usage()
	}

	if strings.TrimSpace(cwd) == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}

	sessionID := strings.TrimSpace(resume)
	if sessionID == "" {
		sessionID = fmt.Sprintf("mock-%d", time.Now().UnixNano())
	}

	if jsonOut {
		emitJSONL(os.Stdout, codexEvent{Type: "thread.started", ThreadID: sessionID})
	}

	// Extract /MockRunCommand directives from the prompt.
	re := regexp.MustCompile(`(?m)^[ \t]*/MockRunCommand[ \t]+(.+)$`)
	matches := re.FindAllStringSubmatch(prompt, -1)

	type cmdResult struct {
		command string
		output  string
		err     error
	}
	results := make([]cmdResult, 0, len(matches))

	const maxOutputBytes = 5 * 1024 * 1024

	for _, m := range matches {
		cmdStr := strings.TrimSpace(m[1])
		if cmdStr == "" {
			continue
		}
		if jsonOut {
			emitJSONL(os.Stdout, codexEvent{
				Type: "item.started",
				Item: &codexItem{Type: "command_execution", Command: cmdStr},
			})
		}

		var shell string
		var shellArgs []string
		if runtime.GOOS == "windows" {
			shell = "cmd.exe"
			shellArgs = []string{"/C", cmdStr}
		} else {
			shell = "sh"
			shellArgs = []string{"-c", cmdStr}
		}

		cmd := exec.Command(shell, shellArgs...)
		cmd.Dir = cwd

		var outBuf limitedBuffer
		outBuf.limit = maxOutputBytes
		cmd.Stdout = &outBuf
		cmd.Stderr = &outBuf

		err := cmd.Run()
		out := outBuf.String()

		results = append(results, cmdResult{
			command: cmdStr,
			output:  out,
			err:     err,
		})

		if jsonOut {
			emitJSONL(os.Stdout, codexEvent{
				Type: "item.completed",
				Item: &codexItem{Type: "command_execution", Command: cmdStr, Text: out},
			})
		}

		if err != nil {
			msg := fmt.Sprintf("command failed: %s", cmdStr)
			var ee *exec.ExitError
			if errors.As(err, &ee) && ee.ProcessState != nil {
				msg = fmt.Sprintf("command failed (exit code %d): %s", ee.ProcessState.ExitCode(), cmdStr)
			}
			if strings.TrimSpace(out) != "" {
				msg = msg + "\n" + strings.TrimSpace(out)
			}
			if jsonOut {
				emitJSONL(os.Stdout, codexEvent{
					Type: "turn.failed",
					Error: &struct {
						Message string `json:"message"`
					}{Message: msg},
				})
			} else {
				_, _ = fmt.Fprintln(os.Stderr, msg)
			}
			os.Exit(1)
		}
	}

	var reply strings.Builder
	if len(results) == 0 {
		reply.WriteString("Mock completed (no commands).")
	} else {
		reply.WriteString("Mock completed.\n\n")
		for i, r := range results {
			fmt.Fprintf(&reply, "Command %d: %s\n", i+1, r.command)
			if strings.TrimSpace(r.output) != "" {
				reply.WriteString(r.output)
				if !strings.HasSuffix(r.output, "\n") {
					reply.WriteString("\n")
				}
			}
			reply.WriteString("\n")
		}
	}

	if jsonOut {
		emitJSONL(os.Stdout, codexEvent{
			Type: "item.completed",
			Item: &codexItem{Type: "agent_message", Text: strings.TrimRight(reply.String(), "\n")},
		})
	} else {
		_, _ = fmt.Fprintln(os.Stdout, reply.String())
	}
}
