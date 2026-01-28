# Codex harness reports errors even when Codex succeeds

## Symptom

When using the `codex` harness, Subtask can report a final network error (and mark the run as failed) even though the worker produced a valid final reply.

## Likely cause (in Subtask code)

`pkg/harness/codex.go` latches *any* streamed JSONL `"error"` event into `Result.Error`, and `runCodexCommand` returns an error at the end of the run whenever `Result.Error` is non-empty — even if:

- the Codex process exits with code 0, and
- the `-o` output file contains a valid final assistant message.

Key spots:

- `processCodexJSONLLine`: `case "error": result.Error = event.Message`
- `runCodexCommand`: `// If we got an error event, return it even if exit code was 0`

This matches the hypothesis that a transient/recovered error (e.g. an internally retried network failure) can poison the final result.

## Why it looks like a “session end” error

`cmd/subtask/send.go` treats any non-nil harness error as a failed worker run regardless of whether a reply was returned, so a latched transient `"error"` event becomes the final outcome for the run.

