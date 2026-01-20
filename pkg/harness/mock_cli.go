package harness

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// MockCLIHarness implements Harness by spawning the `subtask-mock-worker` binary.
//
// It intentionally uses a Codex-like JSONL stream so we can reuse the existing
// parseCodexExecJSONL plumbing (session start, tool calls, final reply).
type MockCLIHarness struct {
	cli cliSpec
}

func (m *MockCLIHarness) Run(ctx context.Context, cwd, prompt, continueFrom string, cb Callbacks) (*Result, error) {
	args := []string{"exec", "--json"}
	if strings.TrimSpace(continueFrom) != "" {
		args = append(args, "--resume", strings.TrimSpace(continueFrom))
	}
	args = append(args, prompt)

	cmd, err := commandForCLI(ctx, m.effectiveCLI(), args)
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
		return nil, fmt.Errorf("failed to start subtask-mock-worker: %w", err)
	}

	result := &Result{}

	var stderrBuf strings.Builder
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(io.MultiWriter(&stderrBuf, os.Stderr), stderr)
	}()

	streamErr := parseCodexExecJSONL(stdout, result, cb, codexMaxJSONLLineBytes)

	cmdErr := cmd.Wait()
	<-stderrDone

	if streamErr != nil && result.Error == "" {
		result.Error = streamErr.Error()
	}
	if result.Error != "" {
		return result, fmt.Errorf("subtask-mock-worker error: %s", result.Error)
	}

	if cmdErr != nil {
		result.Error = strings.TrimSpace(stderrBuf.String())
		if result.Error == "" {
			result.Error = cmdErr.Error()
		}
		return result, fmt.Errorf("subtask-mock-worker failed: %w", cmdErr)
	}

	if strings.TrimSpace(result.Reply) == "" {
		var parts []string
		parts = append(parts, "subtask-mock-worker produced empty reply")
		if streamErr != nil {
			parts = append(parts, fmt.Sprintf("json stream error: %v", streamErr))
		}
		if s := strings.TrimSpace(stderrBuf.String()); s != "" {
			parts = append(parts, fmt.Sprintf("stderr: %s", s))
		}
		result.Error = strings.Join(parts, "; ")
		return result, fmt.Errorf("subtask-mock-worker failed: %s", result.Error)
	}

	return result, nil
}

func (m *MockCLIHarness) Review(cwd string, target ReviewTarget, instructions string) (string, error) {
	prompt := buildReviewPrompt(cwd, target, instructions)
	result, err := m.Run(context.Background(), cwd, prompt, "", Callbacks{})
	if err != nil {
		return "", err
	}
	return result.Reply, nil
}

func (m *MockCLIHarness) MigrateSession(sessionID, oldCwd, newCwd string) error {
	return nil
}

func (m *MockCLIHarness) DuplicateSession(sessionID, oldCwd, newCwd string) (string, error) {
	return newUUIDv4()
}

func (m *MockCLIHarness) effectiveCLI() cliSpec {
	if strings.TrimSpace(m.cli.Exec) == "" {
		return cliSpec{Exec: "subtask-mock-worker"}
	}
	return m.cli
}
