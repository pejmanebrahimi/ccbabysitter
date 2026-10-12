package procs

import (
	"os/exec"
	"runtime"
	"testing"
)

// A Unix creation time is the boot time plus the ticks since boot, and the
// boot time is only known to the second, so it can move by a second when
// the clock is synced after boot. Exists allows for that there, and holds
// a Windows creation time, which is read exactly, to a tight window.
func TestExistsToleranceFollowsTheSystem(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		if got := existsToleranceMs(goos); got <= 1000 {
			t.Errorf("%s: tolerance %d ms does not cover a second's shift", goos, got)
		}
	}
	if got := existsToleranceMs("windows"); got != filetimeToleranceMs {
		t.Errorf("windows: tolerance %d ms, want %d", got, filetimeToleranceMs)
	}
}

// The same process, its creation time read a second off, still exists on
// Unix; a creation time five seconds off is another process.
func TestExistsAllowsASecondsShift(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a Windows creation time is exact")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	r := NewReal()
	ct, ok := r.CreateTime(cmd.Process.Pid)
	if !ok {
		t.Fatal("child create time")
	}
	if !r.Exists(cmd.Process.Pid, ct+1000) {
		t.Error("a creation time a second off was taken for another process")
	}
	if r.Exists(cmd.Process.Pid, ct+5000) {
		t.Error("a creation time five seconds off was taken for the same process")
	}
}
