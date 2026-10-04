package procs

import (
	"testing"
	"time"
)

// The boot time is in the past, and not before this test process could
// have been started by anything at all.
func TestBootTimeIsThisBoot(t *testing.T) {
	boot := BootTime()
	now := uint64(time.Now().Unix())
	if boot == 0 || boot > now {
		t.Fatalf("boot time %d, now %d", boot, now)
	}
}
