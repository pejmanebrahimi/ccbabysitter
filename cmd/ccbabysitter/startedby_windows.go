//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"

	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// backgroundExe is the windowless program the release ships beside
// ccbabysitter.exe: the same code built as a Windows GUI program, so
// Windows opens no console window for it, at a start from the launcher or
// at login.
const backgroundExe = "ccbabysitter-background.exe"

// exeName is the path this program was started as. Tests replace it.
var exeName = func() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return os.Args[0]
}

// startedByServiceManager reports whether this run is the windowless
// program, which is always the background copy, however it was started:
// it has no console for a launcher to print to.
func startedByServiceManager() bool {
	return strings.EqualFold(filepath.Base(exeName()), backgroundExe)
}

// notStartedLine is what the launcher says when the copy it started never
// answered, with where to look on this system.
func notStartedLine() string {
	return "CC Babysitter did not start. See its log: " + filepath.Join(state.DefaultDir(), "log.txt")
}
