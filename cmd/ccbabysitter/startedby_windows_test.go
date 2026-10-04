//go:build windows

package main

import (
	"strings"
	"testing"
)

// The windowless program is the background copy, however it is started;
// the console program never is.
func TestTheBackgroundProgramIsTheService(t *testing.T) {
	saved := exeName
	t.Cleanup(func() { exeName = saved })
	for name, want := range map[string]bool{
		`C:\Users\a\AppData\Local\Programs\CCBabysitter\ccbabysitter-background.exe`: true,
		`C:\x\CCBABYSITTER-BACKGROUND.EXE`:                                           true,
		`C:\x\ccbabysitter.exe`:                                                      false,
	} {
		exeName = func() string { return name }
		if got := startedByServiceManager(); got != want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestNotStartedLineOnWindowsNamesTheLog(t *testing.T) {
	if line := notStartedLine(); !strings.Contains(line, "log.txt") || strings.Contains(line, "journalctl") {
		t.Fatalf("%q", line)
	}
}
