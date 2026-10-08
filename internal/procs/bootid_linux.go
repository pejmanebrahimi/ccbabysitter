package procs

import (
	"os"
	"strings"
)

// BootID is the id Linux gives this boot, which changes at every boot and
// never with the clock, or "" when it cannot be read.
func BootID() string {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
