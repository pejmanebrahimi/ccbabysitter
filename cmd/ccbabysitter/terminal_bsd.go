//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package main

import "syscall"

// ioctlGetTermios is the request that reads a terminal's settings.
const ioctlGetTermios = syscall.TIOCGETA
