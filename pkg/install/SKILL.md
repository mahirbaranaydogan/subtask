---
name: subtask
description: "Parallel task orchestration CLI that dispatches work to AI workers in isolated git workspaces. Use when the user wants to draft, create, run, or manage tasks, delegate tasks to workers/subagents, or mentions subtask or Subtask."
---

# Subtask

Subtask is a CLI for orchestrating parallel AI workers. There are three roles: the user who gives direction, you (the lead) who orchestrates and delegates, and workers who execute tasks.

Each worker runs in an isolated git worktree. They can't conflict with each other.

The user tells you what they need. You clarify requirements, break work into tasks, dispatch to workers, review their output, iterate until it's right, and merge when ready.

Prefer to delegate exploration, research and planning to workers as parts of their tasks. Workers have time & space to dig deep, whereas you should preserve context to lead. Only go into details yourself when user explicitly requested, or the situation calls for it.

## Mindset

1. **Understand before delegating** — ask questions, clarify requirements. Don't rush to create tasks until you understand what the user actually wants.
2. **Own the complexity** — stay on top of all tasks. Surface progress and blockers. Don't make the user chase status.
3. **Work autonomously** — review output, request changes, iterate with workers. Only involve the user for decisions they need to make.
4. **Ask before merging** — get user sign-off before merging. Don't merge without user approval.

## Commands

| Command | Description |
|---------|-------------|
| `subtask ask "..."` | Quick question (no task, runs in cwd) |
| `subtask draft <task> --base-branch <branch> --title "..." <<'EOF'` | Create a task |
| `subtask send <task> <prompt>` | Prompt worker on task (blocks until reply) |
| `subtask send --detach <task> <prompt>` | Start worker in the background and return immediately |
| `subtask stage <task> <stage>` | Advance workflow stage |
| `subtask list` | View all tasks |
| `subtask show <task>` | View task details |
| `subtask diff [--stat] <task>` | Show changes (from merge base) |
| `subtask merge <task> -m "msg"` | Squash-merge task into base branch |
| `subtask close <task>` | Close without merging, free workspace |
| `subtask workspace <task>` | Get workspace path (a git worktree) |
| `subtask interrupt <task>` | Gracefully stop a running worker |
| `subtask log <task>` | Show task conversation and history |
| `subtask trace <task>` | Debug what a worker is doing and thinking internally |
| `subtask codex-bridge bind --lead <name> --session <id> --task-prefix <prefix>` | Route worker replies to a Codex lead owner |
| `subtask codex-bridge watch` | Watch worker replies and notify or resume bound Codex leads |

**Tip:** Add `--follow-up <task>` on `draft` to carry forward conversation context from a prior task.

## Flow

```bash
# 1. Draft (task name is branch name, task description is shared with worker)
subtask draft fix/bug --base-branch main --title "Fix worker pool panic" <<'EOF'
There's an intermittent panic in the worker pool under high concurrency—likely a race condition in pool.go.
Reproduce, find root cause, fix, and add tests.
EOF

# 2. Start the worker
subtask send fix/bug "Go ahead."

# 3. When worker finishes, review and iterate
subtask stage fix/bug review
# Review with `subtask diff --stat fix/bug`, or read the files at `cd $(subtask workspace fix/bug)`.

# 4. Request changes if needed
subtask send fix/bug <<'EOF'
Also handle the edge case when pool is empty.
EOF

# 5. When ready, merge or close
subtask stage fix/bug ready
subtask merge fix/bug -m "Fix race condition in worker pool"
# Or if not merging: subtask close fix/bug
```

**Critical for Codex:** Desktop notifications do not wake a visible Codex CLI thread by themselves. For Codex-led Subtask work, bind tasks or task prefixes to the correct lead owner first, then keep `subtask codex-bridge watch` running. The bridge defaults to safe `notify` delivery: it records the reply and sends a desktop notification without running hidden Codex work. For hands-off work where the lead must continue without the user sitting at the terminal, bind with `--delivery exec-resume`; this uses `codex exec resume <session>` to wake the bound Codex session in the background. The bridge never merges automatically.

## Merging

`subtask merge` squashes all task commits into a single commit on the base branch.

```bash
subtask merge fix/bug -m "Fix race condition"
```

**If conflicts occur**, merge will fail with instructions. Follow them.

## Stages

All tasks have stages: `doing → review → ready`

| Stage | When to advance |
|-------|-----------------|
| `doing` | Worker is working (default) |
| `review` | Worker done, you're reviewing code |
| `ready` | Ready for human to decide (human review, merge, more work, etc.) |

Advance with: `subtask stage <task> <stage>`

## Planning Workflows

For complex tasks, add a plan stage: `plan → implement → review → ready`

**You plan (`--workflow you-plan`):** You draft PLAN.md in task folder, worker reviews and pokes holes.
**They plan (`--workflow they-plan`):** Worker drafts PLAN.md in task folder, you review and approve or request changes.

If the user asks for `plan → implement → review`, or says the same worker should plan and then implement because it owns the context, draft with `--workflow they-plan`. Do not simulate this by naming the task `*-plan` while using the default workflow.

Correct:
```bash
subtask draft feature/name --base-branch main --workflow they-plan --title "Build feature name" <<'EOF'
...
EOF
```

After the worker replies with the plan:
1. Review `PLAN.md`.
2. Request changes if needed with `subtask send <task> "..."`
3. Only after approving the plan, run `subtask stage <task> implement`.
4. Then send the implementation prompt to the same task/worker context.
5. Do not merge until the task reaches `ready`.

## Codex Lead Bridge

When using Codex as the lead, bind each task or task family to the Codex session that owns it:

```bash
subtask codex-bridge bind --lead backend-lead --session 019d... --task-prefix backend/ --from-now
subtask codex-bridge bind --lead pos-lead --session 019d... --task-prefix apps-pos/ --from-now
subtask codex-bridge watch
```

Default delivery is `notify`. It is the safest mode for visible CLI workflows, but it does not continue the lead automatically:

```bash
subtask codex-bridge bind --lead backend-lead --session 019d... --task-prefix backend/ --delivery notify --from-now
```

Use `--delivery exec-resume` when the user explicitly wants hands-off wakeup and accepts that the resumed Codex work may run in the background rather than inside the currently visible terminal pane. Bridge resumes run with app/plugin MCP tools disabled so background wakeups avoid unrelated connector auth prompts and stay focused on Subtask shell review. A bridge resume should do one focused pass and then stop; if it needs to send follow-up to a worker, use `subtask send --detach ...` so the bridge watcher does not block:

```bash
subtask codex-bridge bind --lead backend-lead --session 019d... --task-prefix backend/ --delivery exec-resume --from-now
```

Routing rules:
- Exact task bindings beat prefix bindings.
- Longest matching prefix wins.
- A worker reply is delivered once per run ID.
- Multiple leads are supported, but each task should have one owner.
- Use `--from-now` when binding an existing project so old replies do not wake the lead.
- `notify` delivery queues the reply for the owning lead and sends a desktop notification.
- `exec-resume` delivery sends a desktop notification and resumes the owning lead for review/stage/follow-up only; merge still needs user approval.
- During a bridge resume, plain `subtask send` auto-detaches so the watcher is not stuck waiting on a worker reply.

## Notes

- Use `subtask list` to see what’s in flight.
- Use `subtask show <task>` to see progress and details.
- Use `subtask log <task>` to see task conversation and events.
- Use `subtask trace <task>` to debug what a worker is doing and thinking internally.
