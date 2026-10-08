//go:build !linux && !darwin

package procs

// BootID is "" here: this system gives a boot no id, so the boot time is
// all there is to tell a restart by.
func BootID() string { return "" }
