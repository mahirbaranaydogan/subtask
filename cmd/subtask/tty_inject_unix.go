//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package main

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

func injectTerminalInput(ttyPath, text string) error {
	if ttyPath == "" {
		return fmt.Errorf("tty path is required")
	}
	if text == "" {
		return nil
	}
	f, err := os.OpenFile(ttyPath, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", ttyPath, err)
	}
	defer func() { _ = f.Close() }()

	for i := 0; i < len(text); i++ {
		ch := text[i]
		_, _, errno := unix.Syscall(
			unix.SYS_IOCTL,
			f.Fd(),
			uintptr(unix.TIOCSTI),
			uintptr(unsafe.Pointer(&ch)),
		)
		if errno != 0 {
			return fmt.Errorf("inject input into %s: %w", ttyPath, errno)
		}
	}
	return nil
}
