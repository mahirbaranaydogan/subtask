package harness

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCodexHarnessRun_EmptyReplyIsError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script helper is unix-only")
	}

	tmp := t.TempDir()
	fakeCodex := filepath.Join(tmp, "codex-fake")
	require.NoError(t, os.WriteFile(fakeCodex, []byte(`#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o|--output-last-message)
      out="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

# Minimal JSONL so harness considers the prompt delivered.
printf '{"type":"thread.started","thread_id":"sess-1"}\n'
printf '{"type":"turn.completed"}\n'

if [ "x$CODEX_WRITE_REPLY" = "x1" ] && [ -n "$out" ]; then
  printf 'hello' > "$out"
fi
exit 0
`), 0o700))

	h := &CodexHarness{cli: cliSpec{Exec: fakeCodex}}

	t.Run("empty_output_file_errors", func(t *testing.T) {
		res, err := h.Run(context.Background(), tmp, "prompt", "", Callbacks{})
		require.Error(t, err)
		require.NotNil(t, res)
		require.Contains(t, res.Error, "empty reply")
	})

	t.Run("non_empty_output_file_succeeds", func(t *testing.T) {
		t.Setenv("CODEX_WRITE_REPLY", "1")
		res, err := h.Run(context.Background(), tmp, "prompt", "", Callbacks{})
		require.NoError(t, err)
		require.NotNil(t, res)
		require.Equal(t, "hello", res.Reply)
	})
}

func TestCodexHarnessRun_TransientErrorEventDoesNotFailSuccessfulRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script helper is unix-only")
	}

	tmp := t.TempDir()
	fakeCodex := filepath.Join(tmp, "codex-fake")
	require.NoError(t, os.WriteFile(fakeCodex, []byte(`#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o|--output-last-message)
      out="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

printf '{"type":"thread.started","thread_id":"sess-1"}\n'
printf '{"type":"error","message":"transient network error"}\n'
printf '{"type":"turn.completed"}\n'

if [ -n "$out" ]; then
  printf 'ok' > "$out"
fi
exit 0
`), 0o700))

	h := &CodexHarness{cli: cliSpec{Exec: fakeCodex}}
	res, err := h.Run(context.Background(), tmp, "prompt", "", Callbacks{})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "ok", res.Reply)
	require.Empty(t, res.Error)
	require.False(t, res.TurnFailed)
}

func TestCodexHarnessRun_TurnFailedStillFailsEvenWithReply(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script helper is unix-only")
	}

	tmp := t.TempDir()
	fakeCodex := filepath.Join(tmp, "codex-fake")
	require.NoError(t, os.WriteFile(fakeCodex, []byte(`#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o|--output-last-message)
      out="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

printf '{"type":"thread.started","thread_id":"sess-1"}\n'
printf '{"type":"turn.failed","error":{"message":"hard failure"}}\n'

if [ -n "$out" ]; then
  printf 'partial output' > "$out"
fi
exit 0
`), 0o700))

	h := &CodexHarness{cli: cliSpec{Exec: fakeCodex}}
	res, err := h.Run(context.Background(), tmp, "prompt", "", Callbacks{})
	require.Error(t, err)
	require.NotNil(t, res)
	require.Contains(t, err.Error(), "hard failure")
	require.True(t, res.TurnFailed)
}
