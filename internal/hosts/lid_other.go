//go:build !darwin && !linux && !windows

package hosts

// lidSleeps is false where CC Babysitter cannot read the lid.
func lidSleeps() bool { return false }
