//go:build linux

package main

import "os"

// startedByServiceManager reports whether systemd started this run, which
// it marks with INVOCATION_ID.
func startedByServiceManager() bool { return os.Getenv("INVOCATION_ID") != "" }

// notStartedLine is what the launcher says when the copy it started never
// answered, with where to look on this system.
func notStartedLine() string {
	return "CC Babysitter did not start. See: journalctl --user -u ccbabysitter"
}
