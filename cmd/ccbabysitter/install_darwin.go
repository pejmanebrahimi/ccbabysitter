//go:build darwin

package main

import (
	"fmt"
	"io"
)

// runInstall explains that install, which sets up a systemd service, is a
// Linux affair; on macOS a plain ccbabysitter starts the LaunchAgent.
func runInstall(out io.Writer) int {
	fmt.Fprintln(out, "install sets up a systemd user service and is for Linux. On macOS, run ccbabysitter: it starts CC Babysitter in the background.")
	return 2
}

// runUninstall stops the LaunchAgent and removes its plist, wherever it is.
// The state folder stays.
func runUninstall(out io.Writer) int {
	_, _ = runLaunchctl("bootout", launchdJob())
	fmt.Fprintln(out, "Stopped the CC Babysitter LaunchAgent.")
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
	fmt.Fprintln(out, foregroundHint)
	return rc
}
