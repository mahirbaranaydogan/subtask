package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func sleepCommandForPlatform(seconds int) string {
	if seconds <= 0 {
		seconds = 1
	}
	if runtime.GOOS == "windows" {
		// ping counts include the first immediate reply.
		return "ping -n " + itoa(seconds+1) + " 127.0.0.1 >NUL"
	}
	return "sleep " + itoa(seconds)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [32]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + (n % 10))
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func TestSendCLI_ConcurrentSends_RaceWindowStillWorkingGuard(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrent send CLI test in short mode")
	}
	t.Setenv("SUBTASK_DIR", t.TempDir())

	binPath := buildSubtask(t)
	mockWorkerPath := mockWorkerPathForSubtask(binPath)
	root := setupParallelTestRepo(t, 2, mockWorkerPath)

	taskName := "send/concurrent"

	// Draft task.
	draftCmd := exec.Command(binPath, "draft", taskName, "Test task description",
		"--base-branch", "main", "--title", "Concurrent send test")
	draftCmd.Dir = root
	out, err := draftCmd.CombinedOutput()
	require.NoError(t, err, "draft failed: %s", out)

	// Use a deterministic barrier inside `subtask send` so both processes reach the point
	// after the unlocked state check but before either sets SupervisorPID under lock.
	barrierDir := filepath.Join(t.TempDir(), "send-barrier")
	envBarrier := []string{
		"SUBTASK_TEST_SEND_BARRIER_DIR=" + barrierDir,
		"SUBTASK_TEST_SEND_BARRIER_N=2",
		"SUBTASK_TEST_SEND_BARRIER_TIMEOUT_MS=20000",
	}

	longPrompt := mockPrompt("Do something slowly") + "\n/MockRunCommand " + sleepCommandForPlatform(2)

	type result struct {
		out []byte
		err error
	}
	ch := make(chan result, 2)

	runSend := func() {
		cmd := exec.Command(binPath, "send", taskName, longPrompt)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), envBarrier...)
		out, err := cmd.CombinedOutput()
		ch <- result{out: out, err: err}
	}

	go runSend()
	go runSend()

	r1 := <-ch
	r2 := <-ch

	// Exactly one should succeed.
	if r1.err == nil && r2.err == nil {
		t.Fatalf("expected one send to fail, but both succeeded:\n---1---\n%s\n---2---\n%s", string(r1.out), string(r2.out))
	}
	if r1.err != nil && r2.err != nil {
		t.Fatalf("expected one send to succeed, but both failed:\n---1---\n%s\n---2---\n%s", string(r1.out), string(r2.out))
	}

	// The failing send must fail cleanly with the guard message.
	failOut := ""
	if r1.err != nil {
		failOut = string(r1.out)
	} else {
		failOut = string(r2.out)
	}
	if !strings.Contains(failOut, "still working") {
		t.Fatalf("expected failing send to contain 'still working', got:\n%s", failOut)
	}

	// Barrier should have had both participants.
	ents, readErr := os.ReadDir(barrierDir)
	require.NoError(t, readErr)
	require.GreaterOrEqual(t, len(ents), 2)
}
