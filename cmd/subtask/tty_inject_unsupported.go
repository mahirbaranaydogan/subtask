//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package main

import "fmt"

func injectTerminalInput(ttyPath, text string) error {
	return fmt.Errorf("terminal-inject delivery is not supported on this platform")
}
