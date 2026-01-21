package install

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/zippoxer/subtask/internal/homedir"
)

//go:embed SKILL.md
var embeddedSkill []byte

// SkillStatus describes the installation state of the embedded skill.
type SkillStatus struct {
	Path            string
	Installed       bool
	UpToDate        bool
	EmbeddedSHA256  string
	InstalledSHA256 string
}

// Embedded returns the embedded skill contents.
func Embedded() []byte {
	return bytes.Clone(embeddedSkill)
}

// SkillPath returns the Claude Code skill path for the given base directory (usually the user's home directory).
func SkillPath(baseDir string) string {
	if baseDir == "" {
		return ""
	}
	return filepath.Join(baseDir, ".claude", "skills", "subtask", "SKILL.md")
}

// Install writes the embedded skill to the Claude Code skill location (user scope).
func Install() (string, bool, error) {
	homeDir, err := homedir.Dir()
	if err != nil {
		return "", false, err
	}
	return InstallTo(homeDir)
}

// InstallTo writes the embedded skill to the Claude Code skill location under baseDir (user scope).
func InstallTo(baseDir string) (string, bool, error) {
	return syncSkillTo(baseDir)
}

// InstallToProject writes the embedded skill to the project-scoped Claude Code skill location.
// projectRoot should be the git root of the project.
func InstallToProject(projectRoot string) (string, bool, error) {
	return syncSkillToProject(projectRoot)
}

// ProjectSkillPath returns the Claude Code skill path for project scope.
func ProjectSkillPath(projectRoot string) string {
	if projectRoot == "" {
		return ""
	}
	return filepath.Join(projectRoot, ".claude", "skills", "subtask", "SKILL.md")
}

// Uninstall removes the skill from the Claude Code skill location (user scope).
func Uninstall() (string, error) {
	homeDir, err := homedir.Dir()
	if err != nil {
		return "", err
	}
	return UninstallFrom(homeDir)
}

// UninstallFrom removes the skill from the Claude Code skill location under baseDir.
func UninstallFrom(baseDir string) (string, error) {
	path := SkillPath(baseDir)
	if path == "" {
		return "", errors.New("invalid base directory")
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return path, nil
}

// GetSkillStatus returns installation status for the Claude Code skill location (user scope).
func GetSkillStatus() (SkillStatus, error) {
	homeDir, err := homedir.Dir()
	if err != nil {
		return SkillStatus{}, err
	}
	return GetSkillStatusFor(homeDir)
}

// GetSkillStatusFor returns status for baseDir without consulting environment.
func GetSkillStatusFor(baseDir string) (SkillStatus, error) {
	path := SkillPath(baseDir)
	if path == "" {
		return SkillStatus{}, errors.New("invalid base directory")
	}

	embeddedSHA := sha256Hex(embeddedSkill)
	st := SkillStatus{
		Path:           path,
		Installed:      false,
		UpToDate:       false,
		EmbeddedSHA256: embeddedSHA,
	}

	installed, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return SkillStatus{}, err
	}

	st.Installed = true
	st.InstalledSHA256 = sha256Hex(installed)
	st.UpToDate = bytes.Equal(installed, embeddedSkill)
	return st, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func syncSkillTo(baseDir string) (string, bool, error) {
	path := SkillPath(baseDir)
	if path == "" {
		return "", false, errors.New("invalid base directory")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", false, err
	}

	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, embeddedSkill) {
		return path, false, nil
	}

	if err := os.WriteFile(path, embeddedSkill, 0o644); err != nil {
		return "", false, err
	}
	return path, true, nil
}

func isSkillInstalled(baseDir string) bool {
	path := SkillPath(baseDir)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func syncSkillToProject(projectRoot string) (string, bool, error) {
	path := ProjectSkillPath(projectRoot)
	if path == "" {
		return "", false, errors.New("invalid project root")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", false, err
	}

	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, embeddedSkill) {
		return path, false, nil
	}

	if err := os.WriteFile(path, embeddedSkill, 0o644); err != nil {
		return "", false, err
	}
	return path, true, nil
}
