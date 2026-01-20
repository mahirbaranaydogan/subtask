package main

import (
	"os"
	"path/filepath"

	"github.com/zippoxer/subtask/pkg/git"
)

// ensureGitignore adds /.subtask/ to .gitignore if not already ignored.
func ensureGitignore(repoRoot string) error {
	// Use git check-ignore to see if already ignored (handles all gitignore semantics).
	subtaskDir := filepath.Join(repoRoot, ".subtask")
	if err := git.RunQuiet(repoRoot, "check-ignore", "-q", subtaskDir); err == nil {
		return nil // Already ignored.
	}

	// Append to .gitignore.
	gitignorePath := filepath.Join(repoRoot, ".gitignore")
	pattern := "/.subtask/"

	// Read existing content to check if we need a leading newline.
	content, _ := os.ReadFile(gitignorePath)

	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if len(content) > 0 && content[len(content)-1] != '\n' {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}
	_, err = f.WriteString(pattern + "\n")
	return err
}

