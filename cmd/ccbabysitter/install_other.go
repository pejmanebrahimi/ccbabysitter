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

// runUninstall mirrors runInstall: there is nothing to reverse here since
// install never did anything on this platform.
func runUninstall(out io.Writer) int {
	fmt.Fprintln(out, "uninstall removes the systemd user service, which is for Linux. On macOS and Windows, switch off autostart in its settings instead.")
	return 2
}

// newServiceControl is nil here: a plain run on this platform always
// serves in the foreground.
func newServiceControl() serviceControl { return nil }
