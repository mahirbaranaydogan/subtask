package git

import (
	"strings"
)

// Error is a structured git execution error that avoids leaking raw "exit status N" strings.
type Error struct {
	Dir    string
	Args   []string
	Stderr string
	Cause  error
}

func (e *Error) Error() string {
	args := strings.Join(e.Args, " ")
	if strings.TrimSpace(e.Stderr) != "" {
		return "git " + args + ": " + strings.TrimSpace(e.Stderr)
	}
	return "git " + args + " failed"
}

func (e *Error) Unwrap() error { return e.Cause }

func isNotGitRepoOutput(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "not a git repository") ||
		strings.Contains(s, "not a git repo")
}

