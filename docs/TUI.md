# Subtask TUI Design

A live, interactive terminal interface for monitoring and managing Subtask workers.

## Vision

The Subtask TUI transforms task management from a sequence of commands (`list`, `show`, `diff`) into a single, always-on dashboard. You launch it in a second terminal tab and it becomes your window into what your workers are doing—live.

**Primary audience**: Humans using Claude Code or OpenCode to orchestrate parallel tasks. They want to:
- See at a glance what's happening across all tasks
- Dive deep into any task without leaving the TUI
- Copy task names, workspace paths, or conversation snippets
- Feel in control, even when multiple workers are busy

## Technology

**Bubble Tea** (Charm ecosystem) with full mouse support:
- `bubbletea` - The Elm Architecture for TUIs
- `bubbles` - Pre-built components (viewport, list, spinner)
- `lipgloss` - Styling and layout
- `bubblezone` - Mouse click regions (unlike gh-dash, we support mouse!)
- `glamour` - Markdown rendering for TASK.md, PLAN.md

Colors use standard ANSI that adapt to the terminal theme (light/dark). No configuration needed—it just works like Claude Code.

## Views

The TUI has two main views: **List View** and **Detail View**.

### List View (Default)

What you see on launch. Full screen dedicated to the task list.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ Subtask                                                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│ ● fix/epoch-boundary        working   implement   +42/-8   ◐ 3/5   12 calls│
│ ○ add/claude-harness        replied   review      +156/-23 ● 5/5           │
│   refactor/workspace-pool   draft     —           —        —               │
│ ✗ test/e2e-flaky            error     implement   +12/-4   ◐ 2/4          │
│ ✓ docs/api-reference        merged    —           +89/-12  —               │
│                                                                             │
│                                                                             │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│ ↑↓ navigate  Enter view  m merge  c close  ? help  q quit                  │
└─────────────────────────────────────────────────────────────────────────────┘
```

Clean and focused. Great for quick monitoring at a glance.

### Empty State

When there are no tasks:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ Subtask                                                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│                                                                             │
│                         No tasks yet.                                       │
│                                                                             │
│              Create one with: subtask draft <name>                          │
│                                                                             │
│                                                                             │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│ q quit  ? help                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Detail View

Press `Enter` on a task to see its details. The top row becomes a **compact task strip** for quick switching.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ ● fix/epoch   ○ add/claude   ✗ test/e2e   (draft) refactor/ws   ...        │
├─────────────────────────────────────────────────────────────────────────────┤
│ [1] Overview  [2] Description  [3] Progress  [4] Conversation  [5] Diff    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  fix/epoch-boundary                                          ● working     │
│  ─────────────────────────────────────────────────────────────────────────  │
│                                                                             │
│  Fix edge case where epoch boundary causes duplicate events                │
│                                                                             │
│  Branch     fix--epoch-boundary                                            │
│  Base       main (2 commits behind)                                        │
│  Model      o3 (high reasoning)                                            │
│  Stage      plan → (implement) → review → ready                            │
│  Duration   4m 32s                                                         │
│                                                                             │
│  Progress ◐ 3/5                                                            │
│  ☑ Identify affected code paths                                            │
│  ☑ Add epoch boundary detection                                            │
│  ☐ Update integration tests                                                │
│                                                                             │
│  Last activity: 8s ago (12 tool calls)                                     │
│  +42 lines / -8 lines                                                      │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│ ←→ task  Esc list  1-5 tabs  m merge  c close  ? help  q quit              │
└─────────────────────────────────────────────────────────────────────────────┘
```

- **Compact task strip**: Shows tasks as badges. Use `←` / `→` to switch tasks without returning to the list. Shows `...` when there are more tasks than fit.
- **Tabs**: Switch with `1-5` or `Tab` / `Shift+Tab`
- **Esc**: Return to full list view

## Navigation

### Keyboard (Primary)

| Key | Action |
|-----|--------|
| `↑` / `k` | Previous task |
| `↓` / `j` | Next task |
| `Enter` | View task details |
| `Esc` | Back to list |
| `←` / `→` | Switch task (in detail view) |
| `1-5` | Switch detail tab |
| `Tab` | Next tab |
| `Shift+Tab` | Previous tab |
| `g` / `G` | First / last task |
| `Ctrl+d` / `Ctrl+u` | Page down / up in content |
| `/` | Filter tasks |
| `r` | Refresh now |
| `m` | Merge task |
| `c` | Close task |
| `y` | Copy task name |
| `Y` | Copy workspace path |
| `q` | Quit |
| `?` | Show help |

### Mouse

| Action | Effect |
|--------|--------|
| Click task row | Select task |
| Double-click task | View task details |
| Click tab | Switch to tab |
| Click task badge | Switch to that task |
| Scroll wheel | Scroll content |
| `Shift` + drag | Native text selection |

**Text selection note**: When the TUI captures mouse events, terminals cannot perform native text selection. Hold `Shift` while clicking/dragging to select text. This is universal across all TUIs—we show this hint in the help screen.

## Detail Tabs

### 1. Overview (Default)

The quick summary—everything you need at a glance:
- Status badge with duration (if working)
- Branch and base branch (with "behind" indicator)
- Model and reasoning level
- Stage progression
- Progress checklist (from PROGRESS.json)
- Diff stats (+lines / -lines)
- Last activity timestamp and tool call count
- Error message (if status is error)
- Conflict files (if any)

### 2. Description

Full rendered TASK.md content:
- YAML frontmatter shown as key-value pairs
- Markdown body rendered with glamour
- Scrollable for long descriptions

### 3. Progress

PROGRESS.json steps with context:
- Checkboxes for each step
- Clear visual distinction between done/pending

### 4. History

Task history (from history.jsonl):
- Messages in one style per role
- Lifecycle events inline (opened, stage changes, runs)
- Syntax highlighting for code blocks
- Scrollable, jumps to latest by default

### 5. Diff

File browser with inline diff viewer:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ Files (4)                 │ harness/codex.go                               │
│ ─────────────────────────────────────────────────────────────────────────  │
│ ▸ harness/codex.go  +42   │ @@ -156,7 +156,12 @@ func (h *CodexHarness)   │
│   harness/claude.go +8    │ +    if err := h.validateSession(ctx); err != │
│   task/state.go     -3    │ +        return nil, fmt.Errorf("invalid...   │
│   cmd/subtask/send.go     │ +    }                                         │
│                           │ +                                              │
│                           │      result, err := h.run(ctx, cwd, prompt)   │
│                           │                                                │
│                           │ @@ -201,3 +206,8 @@ func (h *CodexHarness)   │
│                           │      return &Result{Reply: reply}, nil         │
│                           │ +}                                             │
│                           │ +                                              │
│                           │ +func (h *CodexHarness) validateSession(...   │
└─────────────────────────────────────────────────────────────────────────────┘
```

- **Left sidebar**: Changed files with +/- line counts
- **Right pane**: Diff for selected file with syntax highlighting
- **Navigation**: `↑`/`↓` to navigate files, scroll wheel for diff content
- **Click** a file to select it

## Live Refresh

Data refreshes every 2 seconds:
- Task list updates in place
- Current selection preserved
- Active spinners for working tasks
- No flicker—diff-based updates

Manual refresh with `r` key.

## Visual Design

### Status Indicators

| Status | Color | Badge |
|--------|-------|-------|
| `working` | Green (bold) | ● |
| `replied` | Yellow | ○ |
| `error` | Red | ✗ |
| `draft` | Gray | (blank) |
| `closed` | Dim | ✓ |
| `merged` | Purple | ✓ |

### Typography

- Task names in bold
- Timestamps and metadata in dim
- Active tab highlighted
- Progress with visual indicator (◐ ◑ ● ○)

## Actions

From either view, press a key to act on the selected task:

| Key | Action | Confirmation |
|-----|--------|--------------|
| `m` | Merge | "Merge fix/epoch-boundary? (y/n)" |
| `c` | Close | "Close fix/epoch-boundary? (y/n)" |
| `a` | Abandon | "Abandon fix/epoch-boundary? Discards changes. (y/n)" |

## Footer

Always visible, contextual based on current view:

**List view:**
```
↑↓ navigate  Enter view  m merge  c close  ? help  q quit
```

**Detail view:**
```
←→ task  Esc list  1-5 tabs  m merge  c close  ? help  q quit
```

## Launch

```bash
subtask          # Launches TUI (new default)
subtask tui      # Explicit TUI command
subtask list     # Non-interactive output (for scripts)
subtask show X   # Non-interactive task details
```

**First run**: If subtask is not configured, it prints an error and exits. Run `subtask install` first.

The bare `subtask` command (no subcommand) launches the TUI. Existing commands work for scripting.

## Future Enhancements

- **Task creation**: `n` to create new task from TUI
- **Log streaming**: Real-time log output as worker runs
- **Search/filter**: Filter tasks by status, name pattern
- **Notifications**: Desktop notification when task replies
- **Multi-select**: Act on multiple tasks at once

## Implementation

### Package Structure

```
tui/
├── tui.go           # Entry point, tea.NewProgram
├── model.go         # Root model, Init/Update/View
├── keys.go          # Key bindings
├── styles.go        # Lipgloss styles
├── refresh.go       # Data refresh logic
├── components/
│   ├── tasklist/    # Task list component
│   ├── taskstrip/   # Compact task badges
│   ├── tabs/        # Tab bar component
│   ├── overview/    # Overview tab
│   ├── description/ # Description tab (markdown)
│   ├── progress/    # Progress tab
│   ├── conversation/# Conversation tab
│   ├── diff/        # Diff tab (file list + viewer)
│   ├── confirm/     # Confirmation dialog
│   └── help/        # Help overlay
└── context.go       # Shared state (dimensions, config)
```

### Data Flow

1. **Init**: Validate git + config, load tasks, start refresh ticker
2. **Tick**: Every 2s, fetch fresh data via existing task/git packages
3. **Update**: Handle keys, mouse, tick messages
4. **View**: Render based on current view and state

### Integration

Reuses existing code:
- `task.ListAll()` - Get all tasks
- `task.Load()` / `task.LoadState()` - Task data
- `git.DiffStat()` / `git.Diff()` - Diff info
- `render.TaskCard` - Formatting logic (adapt for TUI)

Executes commands via existing Run functions:
- `merge.Run()` - Merge task
- `close.Run()` - Close task

---

## Inspirations

- **gh-dash**: Async refresh, section model
- **lazygit**: Panel navigation, contextual actions
- **k9s**: Live resource monitoring
- **Charm tools**: Modern TUI aesthetics

## Principles

1. **Keyboard-first, mouse-friendly**: Power users navigate with keys, casual users can click
2. **Live by default**: Always showing current state
3. **Progressive disclosure**: List first, details on demand
4. **Non-destructive**: Confirmations for dangerous actions
5. **Copy-friendly**: Easy to grab task names, paths, content
6. **Beautiful defaults**: Looks great out of the box
