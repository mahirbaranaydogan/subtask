---
allowed-tools: Bash(git:*), Bash(gh:*)
argument-hint: [optional - will prompt interactively]
description: Create and publish a new release
---

## Context

- Latest stable: !`git tag -l 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | grep -v '-' | head -1 || echo "none"`
- Latest beta: !`git tag -l 'v*-beta*' --sort=-v:refname | head -1 || echo "none"`
- Current branch: !`git branch --show-current`
- Git status: !`git status --short`
- Commits since last tag: !`git log $(git describe --tags --abbrev=0 2>/dev/null)..HEAD --oneline 2>/dev/null || git log --oneline -5`

## Task

Create a new release.

### Steps

1. **Show current state** to the user:
   - Latest stable version
   - Latest beta version (if any)
   - What versions each release type would create

2. **Ask user what release to create** using AskUserQuestion:
   - Calculate and show concrete version numbers for each option
   - Example options (adjust based on current state):
     - "v0.2.0 (minor)" — requires `main` branch
     - "v0.1.2 (patch)" — requires `main` branch
     - "v1.0.0 (major)" — requires `main` branch
     - "v0.2.0-beta.1 (new beta)" — requires `dev` branch
     - "v0.2.0-beta.2 (next beta)" — only if beta exists, requires `dev` branch
   - If argument was provided (e.g., `/release patch`), skip the question and use it

3. **Check prerequisites**:
   - Working directory is clean
   - Tests pass: `go test ./...`
   - **IMPORTANT - Branch rules:**
     - **Stable releases (major/minor/patch)**: MUST be on `main` branch
     - **Beta releases**: MUST be on `dev` branch
     - If on wrong branch, stop and tell the user to switch branches first

4. **Create and push tag**:
   ```bash
   VERSION=vX.Y.Z  # or vX.Y.Z-beta.N for beta
   git tag "$VERSION"
   git push origin "$VERSION"
   ```

5. **Monitor release workflow**:
   ```bash
   gh run watch --workflow release.yml --interval 10
   ```

6. **Mark as prerelease** (beta only):
   ```bash
   gh release edit "$VERSION" --prerelease
   ```
   This ensures the beta won't be picked up by auto-update (which uses `/releases/latest`).

7. **Verify release**:
   ```bash
   gh release view "$VERSION"
   gh release view "$VERSION" --json assets --jq '.assets[].name'
   ```

8. **Add release notes**:
   - Read the commit history since the last release
   - Group changes by type (Features, Fixes, Improvements, etc.)
   - Write a concise summary highlighting the most important changes
   - Update the release notes:
   ```bash
   gh release edit "$VERSION" --notes "$(cat <<'EOF'
   ## What's New

   ### Features
   - Feature 1
   - Feature 2

   ### Fixes
   - Fix 1
   - Fix 2

   ### Improvements
   - Improvement 1

   **Full Changelog**: https://github.com/zippoxer/subtask/compare/vPREVIOUS...$VERSION
   EOF
   )"
   ```

9. **Verify Homebrew tap updated** (stable releases only - skip for beta):
   ```bash
   gh api "repos/zippoxer/homebrew-tap/contents/Formula/subtask.rb?ref=main" --jq .content \
     | base64 --decode \
     | rg "version|url|sha256" -n
   ```

10. **Test Homebrew install** (stable releases only - skip for beta):
    ```bash
    brew fetch --force zippoxer/tap/subtask
    ```
    This downloads the tarball and verifies the checksum matches the formula.

Note: Do NOT update the local installation. The user tests with local builds (`go install ./cmd/subtask`), not Homebrew.

### Beta Release Notes

For beta releases, keep notes concise and focused on what to test:
```bash
gh release edit "$VERSION" --prerelease --notes "$(cat <<'EOF'
## Beta Release

This is a **prerelease** for testing. Not recommended for production.

### Changes
- Change 1
- Change 2

### Testing
Please report issues at https://github.com/zippoxer/subtask/issues
EOF
)"
```

### Troubleshooting

If release workflow fails:
```bash
gh run list --workflow release.yml --limit 5
gh run view --log-failed <run-id>
```
Fix the issue, then create a NEW tag (don't reuse).

To undo a bad release:
```bash
git tag -d "$VERSION"
git push origin ":refs/tags/$VERSION"
gh release delete "$VERSION" -y
```
