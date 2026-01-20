package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeFakeCLI(t *testing.T, dir string, name string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, name+".bat")
		require.NoError(t, os.WriteFile(path, []byte("@echo off\r\nexit /B 0\r\n"), 0o644))
		return path
	}

	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	return path
}

