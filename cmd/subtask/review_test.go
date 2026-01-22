package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zippoxer/subtask/pkg/harness"
	"github.com/zippoxer/subtask/pkg/testutil"
)

func TestReviewCmd_Task_PassesBaseBranchAndInstructions(t *testing.T) {
	env := testutil.NewTestEnv(t, 1)

	taskName := "review/test"
	env.CreateTask(taskName, "Review test", "main", "Description")

	// First run the task to create a workspace
	sendMock := harness.NewMockHarness().WithResult("Done", "session-1")
	require.NoError(t, (&SendCmd{Task: taskName, Prompt: "Do it"}).WithHarness(sendMock).Run())

	// Now test review
	reviewMock := harness.NewMockHarness().WithReviewResult("No issues found")

	stdout, stderr, err := captureStdoutStderr(t, (&ReviewCmd{
		Task:   taskName,
		Prompt: "Focus on security",
	}).WithHarness(reviewMock).Run)

	require.NoError(t, err)
	require.Empty(t, stderr)
	assert.Contains(t, stdout, "No issues found")

	// Verify the mock received correct arguments
	require.Len(t, reviewMock.ReviewCalls, 1)
	call := reviewMock.ReviewCalls[0]
	assert.NotEmpty(t, call.CWD)
	assert.Equal(t, "main", call.Target.BaseBranch)
	assert.Equal(t, "Focus on security", call.Instructions)
}

func TestReviewCmd_Task_NoInstructions(t *testing.T) {
	env := testutil.NewTestEnv(t, 1)

	taskName := "review/no-instructions"
	env.CreateTask(taskName, "Review test", "main", "Description")

	sendMock := harness.NewMockHarness().WithResult("Done", "session-1")
	require.NoError(t, (&SendCmd{Task: taskName, Prompt: "Do it"}).WithHarness(sendMock).Run())

	reviewMock := harness.NewMockHarness().WithReviewResult("Looks good")

	_, _, err := captureStdoutStderr(t, (&ReviewCmd{
		Task: taskName,
	}).WithHarness(reviewMock).Run)

	require.NoError(t, err)

	require.Len(t, reviewMock.ReviewCalls, 1)
	call := reviewMock.ReviewCalls[0]
	assert.Equal(t, "main", call.Target.BaseBranch)
	assert.Empty(t, call.Instructions)
}

func TestReviewCmd_Uncommitted(t *testing.T) {
	_ = testutil.NewTestEnv(t, 0)

	reviewMock := harness.NewMockHarness().WithReviewResult("No issues")

	stdout, stderr, err := captureStdoutStderr(t, (&ReviewCmd{
		Uncommitted: true,
	}).WithHarness(reviewMock).Run)

	require.NoError(t, err)
	require.Empty(t, stderr)
	assert.Contains(t, stdout, "No issues")

	require.Len(t, reviewMock.ReviewCalls, 1)
	call := reviewMock.ReviewCalls[0]
	assert.True(t, call.Target.Uncommitted)
}

func TestReviewCmd_BaseBranch(t *testing.T) {
	_ = testutil.NewTestEnv(t, 0)

	reviewMock := harness.NewMockHarness().WithReviewResult("No issues")

	stdout, stderr, err := captureStdoutStderr(t, (&ReviewCmd{
		Base: " main ",
	}).WithHarness(reviewMock).Run)

	require.NoError(t, err)
	require.Empty(t, stderr)
	assert.Contains(t, stdout, "No issues")

	require.Len(t, reviewMock.ReviewCalls, 1)
	call := reviewMock.ReviewCalls[0]
	assert.Equal(t, "main", call.Target.BaseBranch)
}

func TestReviewCmd_Commit(t *testing.T) {
	_ = testutil.NewTestEnv(t, 0)

	reviewMock := harness.NewMockHarness().WithReviewResult("Commit looks good")

	stdout, stderr, err := captureStdoutStderr(t, (&ReviewCmd{
		Commit: "abc1234",
	}).WithHarness(reviewMock).Run)

	require.NoError(t, err)
	require.Empty(t, stderr)
	assert.Contains(t, stdout, "Commit looks good")

	require.Len(t, reviewMock.ReviewCalls, 1)
	call := reviewMock.ReviewCalls[0]
	assert.Equal(t, "abc1234", call.Target.Commit)
}

func TestReviewCmd_MutuallyExclusive(t *testing.T) {
	_ = testutil.NewTestEnv(t, 0)

	_, _, err := captureStdoutStderr(t, (&ReviewCmd{
		Base:        "main",
		Uncommitted: true,
	}).Run)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestReviewCmd_RequiresTarget(t *testing.T) {
	_ = testutil.NewTestEnv(t, 0)

	_, _, err := captureStdoutStderr(t, (&ReviewCmd{}).Run)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "specify one of")
}

func TestReviewCmd_TaskNotFound(t *testing.T) {
	_ = testutil.NewTestEnv(t, 0)

	_, _, err := captureStdoutStderr(t, (&ReviewCmd{
		Task: "nonexistent/task",
	}).Run)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load task")
}

func TestReviewCmd_NoWorkspace(t *testing.T) {
	_ = testutil.NewTestEnv(t, 0)

	taskName := "review/no-workspace"

	// Create a draft task without running it
	_, _, err := captureStdoutStderr(t, (&DraftCmd{
		Task:        taskName,
		Description: "Description",
		Base:        "main",
		Title:       "Draft review",
	}).Run)
	require.NoError(t, err)

	// Review should fail because there's no workspace
	_, _, err = captureStdoutStderr(t, (&ReviewCmd{
		Task: taskName,
	}).Run)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no workspace")
	assert.Contains(t, err.Error(), "subtask send")
}
