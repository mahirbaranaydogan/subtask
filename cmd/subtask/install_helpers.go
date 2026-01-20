package main

import (
	"fmt"
	"os"

	"github.com/zippoxer/subtask/internal/homedir"
	"github.com/zippoxer/subtask/pkg/git"
	"github.com/zippoxer/subtask/pkg/install"
)

func parseInstallScope(s string) (install.Scope, error) {
	switch s {
	case "", "user":
		return install.ScopeUser, nil
	case "project":
		return install.ScopeProject, nil
	default:
		return "", fmt.Errorf("invalid scope %q (expected user|project)", s)
	}
}

func projectRootFromCwd() (root string, inGit bool, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false, err
	}

	insideWorkTree, err := git.Output(cwd, "rev-parse", "--is-inside-work-tree")
	if err == nil && insideWorkTree == "true" {
		top, err := git.Output(cwd, "rev-parse", "--show-toplevel")
		if err == nil && top != "" {
			return top, true, nil
		}
		return cwd, true, nil
	}

	return cwd, false, nil
}

func baseDirForScope(scope install.Scope) (baseDir string, inGit bool, err error) {
	switch scope {
	case install.ScopeUser:
		homeDir, err := homedir.Dir()
		if err != nil {
			return "", false, err
		}
		return homeDir, false, nil
	case install.ScopeProject:
		return projectRootFromCwd()
	default:
		return "", false, fmt.Errorf("invalid scope %q", scope)
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
