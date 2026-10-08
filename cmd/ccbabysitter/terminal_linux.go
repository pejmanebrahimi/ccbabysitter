package main

import "syscall"

// ioctlGetTermios is the request that reads a terminal's settings.
const ioctlGetTermios = syscall.TCGETS
