//go:build darwin

package main

import (
	"fmt"
	"io"

	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// runInstall explains that install, which sets up a systemd service, is a
// Linux affair; on macOS a plain ccbabysitter starts the LaunchAgent.
func runInstall(out io.Writer) int {
	fmt.Fprintln(out, "install sets up a systemd user service and is for Linux. On macOS, run ccbabysitter: it starts CC Babysitter in the background.")
	return 2
}

// uninstallSystem is what uninstall does on macOS: the program is one file
// in a folder it may share with others, and nothing changes PATH.
func uninstallSystem() uninstallSteps {
	return uninstallSteps{removeStart: removeStartAtLogin, removeFiles: removeFilesNow}
}

// removeStartAtLogin stops the LaunchAgent and removes its plist, wherever
// it is. A later plain run turns start at login on again.
func removeStartAtLogin(out io.Writer) int {
	if _, err := runLaunchctl("bootout", launchdJob()); err != nil {
		fmt.Fprintln(out, "The CC Babysitter LaunchAgent was not running.")
	} else {
		fmt.Fprintln(out, "Stopped the CC Babysitter LaunchAgent.")
	}
	rc := 0
	for _, path := range []string{agentPath(), offPath()} {
		if on, _ := pathPresent(path); !on || path == "" {
			continue
		}
		if err := removeDurable(path); err != nil {
			fmt.Fprintln(out, "could not remove", path+":", err)
			rc = 1
			continue
		}
		fmt.Fprintln(out, "Removed", path)
	}
	// A later plain run turns start at login on again, as on a machine
	// that never had CC Babysitter.
	_ = state.ForgetLoginStartOffered(state.DefaultDir())
	return rc
}
