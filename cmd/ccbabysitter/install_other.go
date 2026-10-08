//go:build !linux && !darwin && !windows

package main

import (
	"fmt"
	"io"
)

// runInstall explains that the systemd based install is a Linux-only
// affair on every other platform, rather than pretending to do something
// it cannot.
func runInstall(out io.Writer) int {
	fmt.Fprintln(out, "install sets up a systemd user service and is for Linux. On macOS and Windows, start CC Babysitter yourself or switch on autostart in its settings.")
	return 2
}

// uninstallSystem is what uninstall does here: the program is one file,
// nothing changes PATH, and there is no start at login to remove.
func uninstallSystem() uninstallSteps {
	return uninstallSteps{removeStart: removeStartAtLogin, removeFiles: removeFilesNow}
}

// removeStartAtLogin has nothing to do on this platform, where CC
// Babysitter never starts at login.
func removeStartAtLogin(out io.Writer) int {
	fmt.Fprintln(out, "There is no start at login to remove on this system.")
	return 0
}

// newServiceControl is nil here: a plain run on this platform always
// serves in the foreground.
func newServiceControl() serviceControl { return nil }
