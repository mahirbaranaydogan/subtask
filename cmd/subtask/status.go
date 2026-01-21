package main

import (
	"github.com/zippoxer/subtask/pkg/install"
	"github.com/zippoxer/subtask/pkg/render"
)

// StatusCmd implements 'subtask status'.
type StatusCmd struct{}

func (c *StatusCmd) Run() error {
	st, err := install.GetSkillStatus()
	if err != nil {
		return err
	}

	skillInstalled := "no"
	skillUpToDate := "-"
	skillSHA := "-"
	if st.Installed {
		skillInstalled = "yes"
		skillUpToDate = yesNo(st.UpToDate)
		if st.InstalledSHA256 != "" {
			skillSHA = shortHash(st.InstalledSHA256)
		}
	}

	kv := &render.KeyValueList{
		Pairs: []render.KV{
			{Key: "Skill path", Value: abbreviatePath(st.Path)},
			{Key: "Skill installed", Value: skillInstalled},
			{Key: "Skill up-to-date", Value: skillUpToDate},
			{Key: "Skill embedded SHA256", Value: shortHash(st.EmbeddedSHA256)},
			{Key: "Skill installed SHA256", Value: skillSHA},
		},
	}
	kv.Print()
	return nil
}

func shortHash(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
