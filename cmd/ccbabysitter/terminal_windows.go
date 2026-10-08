package main

import (
	"os"
	"syscall"
)

// isTerminal reports whether f is a console: only a console has a console
// mode. The NUL device is a character device too, so its file type cannot
// tell.
func isTerminal(f *os.File) bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode) == nil
}
