package main

import (
	"github.com/zippoxer/subtask/pkg/task"
	taskmigrate "github.com/zippoxer/subtask/pkg/task/migrate"
	"github.com/zippoxer/subtask/pkg/workspace"
)

type preflightProjectResult struct {
	RepoRoot string
	Config   *workspace.Config
}

func preflightProject() (*preflightProjectResult, error) {
	repoRoot, err := task.GitRootAbs()
	if err != nil {
		return nil, err
	}
	if err := taskmigrate.EnsureLayout(repoRoot); err != nil {
		return nil, err
	}
	cfg, err := workspace.LoadConfig()
	if err != nil {
		return nil, err
	}
	return &preflightProjectResult{RepoRoot: repoRoot, Config: cfg}, nil
}

func preflightProjectOnly() (string, error) {
	repoRoot, err := task.GitRootAbs()
	if err != nil {
		return "", err
	}
	if err := taskmigrate.EnsureLayout(repoRoot); err != nil {
		return "", err
	}
	return repoRoot, nil
}

