package main

import (
	"os"
	"path/filepath"

	"github.com/zippoxer/subtask/internal/homedir"
	"github.com/zippoxer/subtask/pkg/install"
	"github.com/zippoxer/subtask/pkg/task"
)

func runAutoUpdate() {
	if os.Getenv(autoUpdateEnvVar) == "1" {
		return
	}

	homeDir, err := homedir.Dir()
	if err == nil && homeDir != "" {
		res, err := install.AutoUpdateIfInstalled(homeDir)
		if err == nil && res.UpdatedSkill {
			printSuccess("Updated skill to latest version")
		}
	}

	repoRoot, err := task.GitRootAbs()
	if err != nil || repoRoot == "" {
		return
	}

	st, err := install.GetSkillStatusFor(repoRoot)
	if err != nil {
		return
	}
	if st.Installed && !st.UpToDate {
		printWarning("Project skill at " + filepath.Join(".claude", "skills", "subtask", "SKILL.md") + " is outdated. Run `subtask install --scope project` to update.")
	}
}
