# History writes before harness acknowledges delivery

## Problem

When sending a message to a worker, we write to `history.jsonl` BEFORE the harness confirms prompt delivery.

Current flow in `cmd/subtask/send.go`:
1. `prepareWorkspaceAndState()` writes `lead.message` + `worker.started` to history
2. `h.Run()` sends to harness

If the harness fails to deliver (network error before prompt is received), history shows "message sent" but the worker never got it.

## Consequences

- **History is misleading**: Shows messages that were never delivered
- **Duplicate messages on retry**: User might re-send, not knowing original was recorded
- **Session continuation ambiguity**: Depends on harness implementation whether it resends

## Current behavior rationale

History records "what lead intended to send" rather than "what was acknowledged." This is useful for:
- Audit/debugging (see what was attempted)
- Crash recovery (know what was in flight)

## Alternative: Delivery-confirmed semantics

To guarantee history only contains delivered messages:

1. Call harness first
2. Wait for `PromptDelivered` confirmation (e.g., `thread.started` event)
3. Only then write to history
4. Handle partial failures (harness started but didn't complete)

## Considerations

- **Complexity**: Partial failure handling is tricky (started but crashed mid-run)
- **Ordering**: Would need to buffer the message until delivery confirmed
- **Atomicity**: What if we confirm delivery, then crash before writing history?

## Recommendation

Keep current behavior but:
1. Document that history records intent, not confirmed delivery
2. Consider adding a `delivered: true/false` field to `lead.message` events
3. Set `delivered: true` after harness confirms (could be done post-hoc)

This preserves the audit trail while adding delivery status visibility.
