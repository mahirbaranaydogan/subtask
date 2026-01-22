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
```bash
Install Subtask with `subtask install --guide`.
```

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

## Updating
```bash
subtask update --check
subtask update
```

## Subtask is Built with Subtask
- I use Claude Code to lead the development (i talk, it creates tasks and tracks everything)
- I use Codex for subagents (just preference, Claude Code works too)
- ~60 tasks merged in the past week
- Proof: https://github.com/user-attachments/assets/6c71e34f-b3c6-4372-ac25-dd3eea15932e


## License

MIT
