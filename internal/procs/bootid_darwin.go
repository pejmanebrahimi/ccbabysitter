package procs

import "syscall"

// BootID is the id macOS gives this boot, which changes at every boot and
// never with the clock, or "" when it cannot be read.
func BootID() string {
	id, err := syscall.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return ""
	}
	return id
}
