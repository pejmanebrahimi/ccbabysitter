package procs

import (
	"runtime"
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

// Linux and macOS give every boot an id of its own, which stays the same
// for as long as the computer is up. Other systems give none.
func TestBootIDIsThisBoot(t *testing.T) {
	id := BootID()
	switch runtime.GOOS {
	case "linux", "darwin":
		if id == "" || BootID() != id {
			t.Fatalf("boot id %q, then %q", id, BootID())
		}
	default:
		if id != "" {
			t.Fatalf("boot id %q on %s", id, runtime.GOOS)
		}
	}
}
