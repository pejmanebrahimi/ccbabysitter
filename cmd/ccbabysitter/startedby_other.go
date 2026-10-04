//go:build !linux && !darwin && !windows

package main

// startedByServiceManager reports whether a service manager started this
// run; none hands CC Babysitter to one here.
func startedByServiceManager() bool { return false }

// notStartedLine is what the launcher says when the copy it started never
// answered.
func notStartedLine() string { return "CC Babysitter did not start." }
