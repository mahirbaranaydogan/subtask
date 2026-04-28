# Subtask ✨

Subtask gives Claude Code a Skill and CLI to create tasks, spawn subagents, track progress, review and request changes.

* Each task gets a Git worktree, so they can be **done in parallel** safely
* **Claude can interrupt and talk with subagents**!
* TUI shows progress, diffs & conversations
* Tasks are persisted in folders
* Codex subagents supported
* Codex lead wakeups supported through `codex-bridge`
* Ralph not needed

### Spawning Tasks
```makefile
You: fix the auth bug and add API metrics with Subtask

Claude:
  ├─► Skill(Subtask)
  ├─► Bash(subtask draft fix/auth-bug ...)
  ├─► Bash(subtask draft feat/api-metrics ...)
  └─⏺ Tasks are in progress! I'll get notified when they're done.
```

### Tracking & Merging
```makefile
You: which tasks are open?

Claude:
  ├─► Bash(subtask list)
  │   ┌──────────────────┬─────────┬────────────────────────┐
  │   │ TASK             │ STATUS  │ TITLE                  │
  │   ├──────────────────┼─────────┼────────────────────────┤
  │   │ fix/auth-bug     │ replied │ Fix auth token refresh │
  │   │ feat/api-metrics │ working │ Add API metrics        │
  │   │ feat/ui-redesign │ draft   │ Redesign dashboard     │
  │   └──────────────────┴─────────┴────────────────────────┘
  │
  └─⏺ ui-redesign hasn't started yet — want me to kick it off?

You: lets merge fix/auth-bug.
```

### TUI
Run `subtask` in your terminal to see everything:
<table>
  <tr>
    <td><img width="983" height="603" alt="image (2)" src="https://github.com/user-attachments/assets/fcc4686a-afa1-4168-b141-e54d9286ad4c" /></td>
    <td><img width="985" height="603" alt="image" src="https://github.com/user-attachments/assets/9d6cd9a2-ba80-4e4e-9fdc-fd0f675b124a" />
</td>
  </tr>
</table>

## Setup

> [!NOTE]
> Subtask is in early development. Upcoming releases will simplify installation, solve known bugs, and improve Claude's proficiency.

### Install the CLI

#### Mac/Linux

```bash
curl -fsSL https://subtask.dev/install.sh | bash
```

#### Windows (PowerShell)

```powershell
irm https://subtask.dev/install.ps1 | iex
```

<details>
<summary>Other install methods…</summary>

#### Homebrew

```bash
brew install zippoxer/tap/subtask
```

#### Go

```bash
go install github.com/zippoxer/subtask/cmd/subtask@latest
```

#### Binary

[GitHub Releases](https://github.com/zippoxer/subtask/releases)

</details>

### Install the Skill

Tell Claude Code:
```md
Setup Subtask with `subtask install --guide`.
```
Claude will install the Subtask skill at `~/.claude/skills`, and ask you whether subagents should run Claude, Codex or OpenCode.

<details>
<summary>Or install manually…</summary>

```bash
subtask install

# Tip: Uninstall later with `subtask uninstall`.
```

</details>

### Install the Plugin (Optional)

In Claude Code:
```
/plugin marketplace add zippoxer/subtask
/plugin install subtask@subtask
```
This reminds Claude to use the Subtask skill when it invokes the CLI.

## Use

Talk with Claude Code about what you want done, and then ask it to use Subtask.

Examples:
- `"fix the login bug with Subtask"`
- `"lets do these 3 features with Subtask"`
- `"plan and implement the new API endpoint with Subtask"`

What happens next:
1. Claude Code creates tasks and runs subagents to do them simultaneously.<br/>
2. Claude gets notified when they're done, and reviews the code.<br/>
3. Claude asks if you want to merge, or ask for changes.

## Codex Lead Bridge

Subtask can also be led from Codex. Desktop notifications are useful, but they
do not continue a Codex CLI session by themselves. The Codex bridge records
which Codex lead owns each task or task prefix, watches worker replies, and can
resume the correct Codex session when a worker finishes.

Bind a Codex lead session to a task family:

```bash
subtask codex-bridge bind \
  --lead growth-lead \
  --session 019d... \
  --task-prefix growth-os/ \
  --delivery exec-resume \
  --from-now
```

Run the watcher:

```bash
subtask codex-bridge watch --poll 2s
```

For a persistent macOS setup, run the watcher from a LaunchAgent with the
project repository as its working directory.

Delivery modes:

- `notify` records the worker reply and sends a desktop notification. This is
  safe and visible, but it does not make Codex continue automatically.
- `exec-resume` sends a desktop notification and runs `codex exec resume` for
  the bound session, so the lead can review the reply without the user waiting
  at the terminal. If the resumed lead sends follow-up worker instructions,
  use `subtask send --detach ...` so the bridge can return immediately.

Routing rules:

- Exact task bindings beat prefix bindings.
- Longest matching prefix wins.
- Use `--from-now` when binding an existing project so old replies do not
  replay.
- Multiple Codex leads are supported by assigning different task prefixes to
  different session IDs.

Safety rules:

- Worker replies are delivered once per run ID.
- Per-lead locking prevents two bridge resumes from running for the same lead
  at the same time.
- Bridge state is written atomically under `.subtask/internal/codex-bridge/`.
- Bridge resume disables app/plugin MCP tools to avoid unrelated connector auth
  prompts in background wakeups.
- Bridge resume is designed for one focused pass. It should not poll, sleep, or
  keep the turn open waiting for a worker. Plain `subtask send` auto-detaches in
  bridge resume mode to avoid blocking future wakeups.
- `subtask merge` is blocked inside a Codex bridge wakeup resume. The bridge can
  review, stage, or request follow-up work, but merge should happen from a
  visible lead session after human approval.

## Updating
```bash
subtask update --check
subtask update
```

## Subtask is Built with Subtask
- I use Claude Code to lead the development (i talk, it creates tasks and tracks everything)
- I use Codex for subagents (just preference, Claude Code works too)
- ~60 tasks merged in the past week
- [Proof](https://github.com/user-attachments/assets/6c71e34f-b3c6-4372-ac25-dd3eea15932e)


## License

MIT
