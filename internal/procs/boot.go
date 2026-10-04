package procs

import "github.com/shirou/gopsutil/v4/host"

// BootTime is when this computer booted, in seconds since the Unix epoch,
// or zero when that cannot be read.
func BootTime() uint64 {
	t, err := host.BootTime()
	if err != nil {
		return 0
	}
	return t
}
