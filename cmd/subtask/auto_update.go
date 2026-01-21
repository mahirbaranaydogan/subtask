package main

import (
	"os"

	"github.com/zippoxer/subtask/internal/homedir"
	"github.com/zippoxer/subtask/pkg/install"
)

func runAutoUpdate() {
	if os.Getenv(autoUpdateEnvVar) == "1" {
		return
	}

	homeDir, err := homedir.Dir()
	if err != nil || homeDir == "" {
		return
	}

	res, err := install.AutoUpdateIfInstalled(homeDir)
	if err != nil {
		return
	}

	if res.UpdatedSkill {
		printSuccess("Updated skill to latest version")
	}
}
