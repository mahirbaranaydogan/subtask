# Subtask ✨

Subtask gives Claude Code a Skill and CLI to create tasks, spawn subagents, track progress, review and request changes.

* Each task gets a Git worktree, so they can be **done in parallel** safely
* **Claude can interrupt and talk with subagents**!
* TUI shows progress, diffs & conversations
* Tasks are persisted in folders
* Codex subagents supported
* Ralph not needed

### Spawning Tasks
```makefile
You: fix the auth bug and add API metrics with Subtask

Claude:
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
Run this in your terminal:
```bash
subtask
```
<table>
  <tr>
    <td><img width="983" height="603" alt="image (2)" src="https://github.com/user-attachments/assets/fcc4686a-afa1-4168-b141-e54d9286ad4c" /></td>
    <td><img width="985" height="603" alt="image" src="https://github.com/user-attachments/assets/9d6cd9a2-ba80-4e4e-9fdc-fd0f675b124a" />
</td>
  </tr>
</table>

## Install

### Get the CLI

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

### Install the Claude Code Skill

```bash
subtask install

# Tip: Uninstall later with `subtask uninstall`.
```

> *This asks whether to install to user-scope (`~/.claude/skills`) or project-scope.*
> 
> *Plugin is recommended to remind Claude to load skill when Subtask CLI is used.*

Restart Claude Code.

### Setup Subtask in your Repo

In Claude Code, run `/subtask:setup`.

*Tip: You can set it up manually with `subtask init`.*

## Use

Ask Claude Code to do things:

- "fix the login bug with Subtask"
- "lets do these 3 features in parallel with Subtask"
- "plan and implement the new API endpoint with Subtask"

Claude Code will draft tasks and run them simultaneously in isolated Git worktrees, then help you review and merge the changes.

## Updating
```bash
subtask update --check
subtask update
```

## License

MIT
