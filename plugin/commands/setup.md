---
description: Install and configure Subtask
---

# Setup Subtask

Install Subtask (skill + plugin) and configure it for use in any Git repository.

## Requirements
Check if `git` is installed and if we're inside a Git repository. If not, let the user know that Subtask requires a Git repository and stop.

## Install + configure (global)

```bash
subtask install
```

This runs a one-time install and configuration wizard and writes user defaults to `~/.subtask/config.json`.

## Optional: project overrides

```bash
subtask config --project
```

Use this only if the current repository needs different settings than your global defaults.

## Done

Tell the user:

> Subtask is ready!
>
> Example usage:
> - "fix the login bug with Subtask"
> - "run these 3 features in parallel"
> - "plan and implement the new API endpoint with Subtask"
>
> I'll draft tasks, dispatch workers in isolated workspaces and let you know when they're done.
