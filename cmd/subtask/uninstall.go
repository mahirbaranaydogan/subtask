package main

import (
	"fmt"

	"github.com/zippoxer/subtask/internal/homedir"
	"github.com/zippoxer/subtask/pkg/install"
)

// UninstallCmd implements 'subtask uninstall'.
type UninstallCmd struct {
}

func (c *UninstallCmd) Run() error {
	homeDir, err := homedir.Dir()
	if err != nil {
		return err
	}

	path, err := install.UninstallFrom(homeDir)
	if err != nil {
		return err
	}

	printSuccess(fmt.Sprintf("Removed skill from %s", abbreviatePath(path)))
	return nil
}
