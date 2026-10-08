//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !windows

package main

import "os"

// isTerminal is false where this program cannot ask the system, so a
// question that needs a terminal is never asked there.
func isTerminal(*os.File) bool { return false }
