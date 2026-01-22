package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/kong"
)

var (
	version = "dev"
	commit  = "none"
)

// CLI defines all subtask commands.
type CLI struct {
	Version kong.VersionFlag `help:"Print version information and quit"`

	Install   InstallCmd   `cmd:"" help:"Install Subtask skill (Claude Code) and configure defaults"`
	Config    ConfigCmd    `cmd:"" help:"Edit configuration (user defaults or project overrides)"`
	Uninstall UninstallCmd `cmd:"" help:"Uninstall Subtask skill (Claude Code)"`
	Status    StatusCmd    `cmd:"" help:"Show installation status (skill)"`
	Ask       AskCmd       `cmd:"" help:"Ask a question (no task, runs in cwd)"`
	Draft     DraftCmd     `cmd:"" help:"Create a task without running"`
	Send      SendCmd      `cmd:"" help:"Send a message to a task"`
	Stage     StageCmd     `cmd:"" help:"Set task workflow stage"`
	List      ListCmd      `cmd:"" help:"List all tasks"`
	Show      ShowCmd      `cmd:"" help:"Show task details"`
	Log       LogCmd       `cmd:"" help:"Show task history (messages + events)"`
	Diff      DiffCmd      `cmd:"" help:"Show task diff"`
	Close     CloseCmd     `cmd:"" help:"Close a task and free workspace"`
	Merge     MergeCmd     `cmd:"" help:"Merge task into base branch (marks as merged)"`
	Workspace WorkspaceCmd `cmd:"" help:"Print workspace path for a task"`
	Review    ReviewCmd    `cmd:"" help:"Get an AI code review"`
	Trace     LogsCmd      `cmd:"" help:"Debug worker runs (tool calls, errors)"`
	Logs      LogsCmd      `cmd:"" help:"Alias for trace" hidden:""`
	Interrupt InterruptCmd `cmd:"" aliases:"stop" help:"Gracefully stop a working worker for a task"`
	Update    UpdateCmd    `cmd:"" help:"Update subtask to the latest release"`
}

func main() {
	if !shouldSkipStartupSideEffects(os.Args) {
		runAutoUpdate()
		startBinaryAutoUpdate()
	}

	if len(os.Args) == 1 {
		if err := runTUIWithInitCheck(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	var cli CLI
	versionInfo := version
	if commit != "" && commit != "none" {
		versionInfo = fmt.Sprintf("%s (%s)", version, commit)
	}
	ctx := kong.Parse(&cli,
		kong.Vars{"version": versionInfo},
		kong.Name("subtask"),
		kong.Description("Parallel task orchestration for AI coding agents"),
		kong.UsageOnError(),
	)
	err := ctx.Run()
	ctx.FatalIfErrorf(err)
}

func shouldSkipStartupSideEffects(args []string) bool {
	if len(args) < 3 || args[1] != "install" {
		return false
	}
	for _, a := range args[2:] {
		if a == "--guide" || strings.HasPrefix(a, "--guide=") {
			return true
		}
	}
	return false
}
