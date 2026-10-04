//go:build darwin

package main

import (
	"fmt"
	"os"
)

// parentPID is this process's parent. Tests replace it.
var parentPID = os.Getppid

// startedByServiceManager reports whether launchd started this run as the
// LaunchAgent: launchd is its parent and XPC_SERVICE_NAME names the
// LaunchAgent's label. Such a run is the background copy, also when an
// earlier version's plist starts the program with no --service. A program
// the copy starts in turn inherits XPC_SERVICE_NAME but not the parent.
func startedByServiceManager() bool {
	return parentPID() == 1 && os.Getenv("XPC_SERVICE_NAME") == launchdLabel
}

// notStartedLine is what the launcher says when the copy it started never
// answered, with where to look on this system.
func notStartedLine() string {
	return fmt.Sprintf("CC Babysitter did not start. See what launchd says with: launchctl print gui/%d/%s", os.Getuid(), launchdLabel)
}
