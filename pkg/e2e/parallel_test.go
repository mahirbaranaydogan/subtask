package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/task"
	"github.com/zippoxer/subtask/pkg/workspace"
)

// TestParallelCLI tests parallel CLI invocations to verify race condition handling.
// This spawns actual subtask processes to test file locking and state management.
func TestParallelCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping parallel CLI test in short mode")
	}
	t.Setenv("SUBTASK_DIR", t.TempDir())

	// Build the binary first
	binPath := buildSubtask(t)
	mockWorkerPath := mockWorkerPathForSubtask(binPath)

	// Create test environment
	root := setupParallelTestRepo(t, 4, mockWorkerPath)

	// Draft all 4 tasks first (sequential - no race here)
	for i := 0; i < 4; i++ {
		taskName := taskNameForNum(i)
		cmd := exec.Command(binPath, "draft", taskName, "Test task description",
			"--base-branch", "main", "--title", "Parallel test")
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "draft for task %d failed: %s", i, out)
	}

	// Run all 4 tasks in parallel (test workspace acquisition)
	var wg sync.WaitGroup
	results := make(chan taskResult, 4)

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			result := justRunSubtaskCmd(t, binPath, root, n)
			results <- result
		}(i)
	}

	wg.Wait()
	close(results)

	// Collect results
	var allResults []taskResult
	for r := range results {
		allResults = append(allResults, r)
	}

	// Verify all 4 completed successfully
	successCount := 0
	for _, r := range allResults {
		t.Logf("Task %d output:\n%s", r.taskNum, r.output)
		if r.err == nil {
			successCount++
		} else {
			t.Logf("Task %d failed: %v", r.taskNum, r.err)
		}
	}
	assert.Equal(t, 4, successCount, "all 4 parallel tasks should succeed")

	// Verify all tasks got different workspaces
	workspaces := make(map[string]int)
	for i := 0; i < 4; i++ {
		taskName := taskNameForNum(i)
		state, err := loadStateFromDir(root, taskName)
		if err != nil {
			t.Logf("Could not load state for %s: %v", taskName, err)
			continue
		}
		if prev, exists := workspaces[state.Workspace]; exists {
			t.Errorf("Workspace collision: task %d and task %d both got %s",
				prev, i, state.Workspace)
		}
		workspaces[state.Workspace] = i
	}
	assert.Len(t, workspaces, 4, "all 4 tasks should have unique workspaces")
}

// TestParallelCLI_AllWorkspacesOccupied tests the error when no workspaces available.
func TestParallelCLI_AllWorkspacesOccupied(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping parallel CLI test in short mode")
	}
	t.Setenv("SUBTASK_DIR", t.TempDir())

	binPath := buildSubtask(t)
	mockWorkerPath := mockWorkerPathForSubtask(binPath)
	root := setupParallelTestRepo(t, 2, mockWorkerPath) // Only 2 workspaces

	// Draft all 4 tasks first (sequential)
	for i := 0; i < 4; i++ {
		taskName := taskNameForNum(i)
		cmd := exec.Command(binPath, "draft", taskName, "Test task description",
			"--base-branch", "main", "--title", "Parallel test")
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "draft for task %d failed: %s", i, out)
	}

	// Run 4 tasks in parallel but only 2 workspaces
	var wg sync.WaitGroup
	results := make(chan taskResult, 4)

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			result := justRunSubtaskCmd(t, binPath, root, n)
			results <- result
		}(i)
	}

	wg.Wait()
	close(results)

	// Count successes and failures
	successCount := 0
	failCount := 0
	for r := range results {
		if r.err == nil {
			successCount++
		} else {
			failCount++
		}
	}

	// Should have exactly 2 successes and 2 "no available workspaces" failures
	assert.Equal(t, 2, successCount, "exactly 2 tasks should succeed")
	assert.Equal(t, 2, failCount, "exactly 2 tasks should fail (no workspaces)")
}

// TestParallelSendFollowup tests parallel follow-up sends on different tasks.
func TestParallelSendFollowup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping parallel CLI test in short mode")
	}

	binPath := buildSubtask(t)
	mockWorkerPath := mockWorkerPathForSubtask(binPath)
	root := setupParallelTestRepo(t, 4, mockWorkerPath)

	// First, draft and run 4 tasks sequentially to set them up
	for i := 0; i < 4; i++ {
		result := draftAndRunSubtaskCmd(t, binPath, root, i)
		require.NoError(t, result.err, "initial run for task %d failed: %s", i, result.output)
	}

	// Now send follow-up messages to all 4 in parallel
	var wg sync.WaitGroup
	results := make(chan taskResult, 4)

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			result := sendSubtaskCmd(t, binPath, root, n)
			results <- result
		}(i)
	}

	wg.Wait()
	close(results)

	// All should succeed
	successCount := 0
	for r := range results {
		if r.err == nil {
			successCount++
		} else {
			t.Logf("Send follow-up for task %d failed: %v\nOutput: %s", r.taskNum, r.err, r.output)
		}
	}
	assert.Equal(t, 4, successCount, "all 4 parallel follow-up sends should succeed")

	// Verify tool calls accumulated
	for i := 0; i < 4; i++ {
		taskName := taskNameForNum(i)
		progress, err := loadProgressFromDir(root, taskName)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, progress.ToolCalls, 6, "tool calls should accumulate (3+3)")
	}
}

type taskResult struct {
	taskNum int
	err     error
	output  string
}

func buildSubtask(t *testing.T) string {
	t.Helper()

	// Find module root
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to resolve caller")
	moduleRoot, err := findModuleRoot(thisFile)
	require.NoError(t, err)

	// Build to temp location (also build subtask-mock-worker into the same dir).
	binName := "subtask"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, binName)
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/subtask")
	cmd.Dir = moduleRoot
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "failed to build subtask: %s", out)

	workerName := "subtask-mock-worker"
	if runtime.GOOS == "windows" {
		workerName += ".exe"
	}
	workerPath := filepath.Join(binDir, workerName)
	cmd = exec.Command("go", "build", "-o", workerPath, "./cmd/subtask-mock-worker")
	cmd.Dir = moduleRoot
	out, err = cmd.CombinedOutput()
	require.NoError(t, err, "failed to build subtask-mock-worker: %s", out)

	return binPath
}

func mockWorkerPathForSubtask(subtaskPath string) string {
	name := "subtask-mock-worker"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(filepath.Dir(subtaskPath), name)
}

func findModuleRoot(filePath string) (string, error) {
	dir := filepath.Dir(filePath)
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("could not find go.mod from %s", filePath)
}

func setupParallelTestRepo(t *testing.T, numWorkspaces int, mockWorkerPath string) string {
	t.Helper()

	root := t.TempDir()

	// Init git repo with 'main' as default branch
	run(t, root, "git", "init", "-b", "main")
	run(t, root, "git", "config", "user.email", "test@test.com")
	run(t, root, "git", "config", "user.name", "Test User")
	// Keep Subtask's task/runtime folders out of test repo commits and status.
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".subtask/\n"), 0o644)
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# Test\n"), 0644)
	run(t, root, "git", "add", ".")
	run(t, root, "git", "commit", "-m", "Initial commit")

	// Create .subtask structure
	subtaskDir := filepath.Join(root, ".subtask")
	os.MkdirAll(filepath.Join(subtaskDir, "tasks"), 0755)
	os.MkdirAll(filepath.Join(subtaskDir, "internal"), 0755)

	// Create workspaces as worktrees using standard naming convention
	escapedPath := task.EscapePath(root)
	wsDir := task.WorkspacesDir()
	os.MkdirAll(wsDir, 0755)

	for i := 1; i <= numWorkspaces; i++ {
		wsPath := filepath.Join(wsDir, fmt.Sprintf("%s--%d", escapedPath, i))
		run(t, root, "git", "worktree", "add", "--detach", wsPath)
	}

	// Create config with mock harness (workspaces discovered from disk)
	cfg := &workspace.Config{
		Harness:       "mock",
		MaxWorkspaces: numWorkspaces,
		Options:       map[string]any{"cli": mockWorkerPath},
	}
	cfgData, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(filepath.Join(subtaskDir, "config.json"), cfgData, 0644)

	return root
}

func taskNameForNum(n int) string {
	return "parallel/task-" + string(rune('a'+n))
}

// justRunSubtaskCmd runs an already-drafted task
func justRunSubtaskCmd(t *testing.T, binPath, root string, taskNum int) taskResult {
	t.Helper()

	taskName := taskNameForNum(taskNum)
	cmd := exec.Command(binPath, "send", taskName, mockPrompt("Do something for parallel test"))
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return taskResult{
		taskNum: taskNum,
		err:     err,
		output:  string(out),
	}
}

// draftAndRunSubtaskCmd drafts then runs a task
func draftAndRunSubtaskCmd(t *testing.T, binPath, root string, taskNum int) taskResult {
	t.Helper()

	taskName := taskNameForNum(taskNum)

	// Draft first
	draftCmd := exec.Command(binPath, "draft", taskName, "Test task description",
		"--base-branch", "main", "--title", "Parallel test")
	draftCmd.Dir = root
	if out, err := draftCmd.CombinedOutput(); err != nil {
		return taskResult{
			taskNum: taskNum,
			err:     err,
			output:  string(out),
		}
	}

	// Then run
	runCmd := exec.Command(binPath, "send", taskName, mockPrompt("Do something for parallel test"))
	runCmd.Dir = root
	out, err := runCmd.CombinedOutput()
	return taskResult{
		taskNum: taskNum,
		err:     err,
		output:  string(out),
	}
}

func sendSubtaskCmd(t *testing.T, binPath, root string, taskNum int) taskResult {
	t.Helper()

	taskName := taskNameForNum(taskNum)
	cmd := exec.Command(binPath, "send", taskName, mockPrompt("Continue the work"))
	cmd.Dir = root

	out, err := cmd.CombinedOutput()
	return taskResult{
		taskNum: taskNum,
		err:     err,
		output:  string(out),
	}
}

func mockPrompt(base string) string {
	base = strings.TrimSpace(base)
	return base + "\n" +
		"/MockRunCommand echo toolcall-1\n" +
		"/MockRunCommand echo toolcall-2\n" +
		"/MockRunCommand echo toolcall-3"
}

func loadStateFromDir(root, taskName string) (*task.State, error) {
	escaped := task.EscapeName(taskName)
	path := filepath.Join(task.ProjectsDir(), task.EscapePath(root), "internal", escaped, "state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state task.State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func loadProgressFromDir(root, taskName string) (*task.Progress, error) {
	escaped := task.EscapeName(taskName)
	path := filepath.Join(task.ProjectsDir(), task.EscapePath(root), "internal", escaped, "progress.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var progress task.Progress
	if err := json.Unmarshal(data, &progress); err != nil {
		return nil, err
	}
	return &progress, nil
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s %v failed: %s", name, args, out)
}
