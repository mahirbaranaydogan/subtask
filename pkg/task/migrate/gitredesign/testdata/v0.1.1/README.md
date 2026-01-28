# v0.1.1 fixtures (gitredesign migration)

These fixtures are a small “real-ish” dataset created with the `v0.1.1` CLI (tag `v0.1.1`) and then minimally tweaked to exercise migration edge cases.

## Contents

- `repo.bundle` — a git bundle containing:
  - `main` with 2 commits (`Initial commit`, plus one commit representing a merged task result)
  - `legacy/merged` branch (exists)
  - `legacy/closed-keep` branch (exists)
  - `legacy/closed-delete` branch (deleted)
- `subtask-dir.tar.gz` — a tarball of the repo-local `.subtask/` directory produced by `subtask v0.1.1`:
  - `.subtask/tasks/*` (TASK.md + history.jsonl)
  - `.subtask/internal/*/op.lock`

## Scenarios included

- `legacy/draftonly`
  - Draft-only task (no terminal event)
- `legacy/merged`
  - `task.opened` missing `base_commit`/`base_ref` (forces backfill)
  - `task.merged` exists but has **zero timestamp** and no frozen stats (forces timestamp + stats backfill)
- `legacy/closed-keep`
  - `task.closed` exists but has **zero timestamp** and no frozen stats (forces timestamp + stats backfill)
  - Task branch exists (can backfill `branch_head`)
- `legacy/closed-delete`
  - `task.closed` exists with non-zero timestamp and no frozen stats
  - Task branch deleted (exercises “branch missing” best-effort behavior)

## How these fixtures were created (high level)

1. Create a new git repo and commit `README.md` on `main`.
2. Build `subtask` from tag `v0.1.1` and run `subtask draft ...` for the four tasks above (this generates `.subtask/tasks/*` and `.subtask/internal/*/op.lock`).
3. Create a few git branches/commits to support migration inference.
4. Edit some `history.jsonl` lines to remove `base_commit` and to introduce zero timestamps / missing frozen stats.
5. Export:
   - `git bundle create repo.bundle --all`
   - `tar czf subtask-dir.tar.gz .subtask`

The e2e test `pkg/task/migrate/gitredesign/gitredesign_e2e_test.go` imports these files into a temp dir and runs `EnsureLayout` + `gitredesign.Ensure()` against them.
